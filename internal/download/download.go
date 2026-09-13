package download

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"sync"
	"time"

	"github.com/asmanya/p2p-file-distribution/internal/metainfo"
	"github.com/asmanya/p2p-file-distribution/internal/peer"
	"github.com/asmanya/p2p-file-distribution/internal/piece"
	"github.com/asmanya/p2p-file-distribution/internal/storage"
	"github.com/asmanya/p2p-file-distribution/internal/tracker"
)

// Options holds the per-run settings a caller (currently just cmd/p2pget) supplies on top of the torrent itself.
type Options struct {
	// Port is what we advertise to the tracker and what we actually bind for incoming connections.
	Port int
	// MaxPeers caps how many peer connections run concurrently. Zero means defaultMaxPeers.
	MaxPeers int
	// Seed keeps the download running as a seeder once every piece is in, instead of returning immediately.
	Seed bool
}

// HaveBitfield tracks which pieces are already verified and on disk - the source of truth this client can seed from
// (phase 10) and what resume uses to skip pieces it doesn't need to re-download. It's written from Download's main
// goroutine as pieces complete, and will be read from other goroutines once seeding exists - a mutex is simplest and this
// struct is neither large nor hot enough to need anything fancier.
type HaveBitfield struct {
	mu sync.Mutex
	bf peer.Bitfield
}

// NewHaveBitfield returns a HaveBitfield sized for pieceCount, with every piece initially missing.
func NewHaveBitfield(pieceCount int) *HaveBitfield {
	return &HaveBitfield{bf: make(peer.Bitfield, (pieceCount+7)/8)}
}

// Set marks index as verified and on disk.
func (h *HaveBitfield) Set(index int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.bf.SetPiece(index)
}

// Has reports whether index is verified and on disk.
func (h *HaveBitfield) Has(index int) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.bf.HasPiece(index)
}

// Snapshot returns a copy of the current bitfield, safe to hand to a connection goroutine that will serialize it
// onto the wire - handing out the live slice instead would let a Set() race with the write.
func (h *HaveBitfield) Snapshot() peer.Bitfield {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make(peer.Bitfield, len(h.bf))
	copy(out, h.bf)
	return out
}

// Count returns how many pieces are verified and on disk right now.
func (h *HaveBitfield) Count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.bf.Count()
}

// Download concurrently downloads every piece of tor, one worker goroutine per connected peer, and writes each verified
// piece straight to outputPath at its correct offset. It announces to tc once up front, and again - no more often than the
// tracker's own minimum interval allows - whenever no piece has completed for stallTimeout, so a torrent doesn't get
// stuck forever on whatever peers happened to be in the first response.
//
// Before requesting anything, it verifies whatever data already exists at outputPath and skips pieces that already match
// their expected hash - this is the entire resume mechanism, with no separate metadata file.
func Download(callerCtx context.Context, tor *metainfo.Torrent, tc *tracker.Client, peerID [20]byte, outputPath string, opts Options) error {
	start := time.Now()
	pieceCount := tor.PieceCount()
	progress := NewProgress(pieceCount)

	maxPeers := opts.MaxPeers
	if maxPeers <= 0 {
		maxPeers = defaultMaxPeers
	}

	sf, err := storage.Create(outputPath, tor.TotalLength)
	if err != nil {
		return fmt.Errorf("download: create output file: %w", err)
	}
	defer sf.Close()

	verifiedPieces, err := storage.VerifyExisting(outputPath, pieceCount, tor.PieceLength, tor.TotalLength, tor.PiecesHashes)
	if err != nil {
		return fmt.Errorf("download: verify existing data: %w", err)
	}

	have := NewHaveBitfield(pieceCount)
	completed := 0

	// bytesOwned is how much of the file we actually have on disk, resumed and freshly-downloaded pieces alike -
	// what the tracker's "left" field needs. progress.BytesDownloaded() alone would be wrong here: it only counts
	// bytes fetched over the network *this session*, so a fully-resumed torrent would report the same "left" as
	// a torrent that had downloaded nothing at all.
	var bytesOwned int64

	resultCh := make(chan Result, resultsBufferSize)
	rates := NewRateTracker()
	coordinator := NewCoordinator(pieceCount, tor.PieceLength, tor.TotalLength, tor.PiecesHashes, resultCh, progress, rates)
	seed := &SeedConfig{PieceLength: tor.PieceLength, TotalLength: tor.TotalLength, Have: have, File: sf}

	// already-verified pieces are marked complete before Run starts - nothing to "skip" at request time, the
	// coordinator simply never offers them to anyone
	for i := 0; i < pieceCount; i++ {
		if verifiedPieces[i] {
			length, err := piece.Length(i, pieceCount, tor.PieceLength, tor.TotalLength)
			if err != nil {
				return fmt.Errorf("download: resumed piece %d: %w", i, err)
			}
			coordinator.MarkComplete(i)
			have.Set(i)
			completed++
			bytesOwned += length
			progress.PieceCompleted() // so Percent()/ETA() reflect resumed progress from the start, not just this session's
		}
	}

	if completed > 0 {
		slog.Info("resume: found existing verified data", "pieces_already_done", completed, "pieces_remaining", pieceCount-completed)
	}

	// ctx (not callerCtx) is what the coordinator, every worker, and the listener actually watch. It's decoupled
	// from callerCtx on purpose: the main loop below bridges the two itself, so that a caller-requested shutdown
	// (an OS signal, typically) gets a grace period serviced normally - resultCh still gets read, pieces still
	// get written - rather than the coordinator and every connection dying in the same instant the signal arrives.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer tc.Close()

	// calling workers
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		coordinator.Run(ctx)
	}()

	// Accept incoming connections too, so peers that connect to us can be served from disk - a listener failing
	// to bind (e.g. the port is already taken by another instance) only disables seeding to new inbound peers,
	// it doesn't fail the download: outgoing connections still work exactly as before.
	if listener, err := peer.Listen(fmt.Sprintf(":%d", opts.Port)); err != nil {
		slog.Warn("download: could not start listener, incoming connections disabled", "error", err)
	} else {
		defer listener.Close()
		wg.Add(1)
		go func() {
			defer wg.Done()
			listener.Serve(ctx, func(conn net.Conn) {
				addr, err := netip.ParseAddrPort(conn.RemoteAddr().String())
				if err != nil {
					conn.Close()
					return
				}
				pconn, err := peer.Accept(conn, tor.InfoHash, peerID)
				if err != nil {
					conn.Close()
					return
				}
				serveIncoming(ctx, pconn, addr, pieceCount, coordinator, progress, rates, seed)
			})
			// Serve has stopped accepting, but connections it already handed off are still running - the
			// listener tracks those itself. Counting them on this WaitGroup instead would mean calling Add
			// from inside a handler, which can race with the Wait below it.
			listener.Wait()
		}()
	}

	var mu sync.Mutex // guards connected - touched by both announce() and the stall check
	connected := make(map[netip.AddrPort]bool)
	minReannounceInterval := stallTimeout
	announceInterval := defaultAnnounceInterval

	// sem caps how many outgoing peer connections run at once. A non-blocking acquire that just skips the peer on
	// failure - rather than blocking the announce goroutine until a slot frees up - keeps announce() itself fast,
	// and leaving that peer out of connected means the next announce naturally retries it once a slot opens up.
	sem := make(chan struct{}, maxPeers)

	// announce reports our real progress to the tracker and starts a worker for each new peer it returns. Safe
	// to call more than once; event is EventNone for an ordinary periodic re-announce, and EventStarted/
	// EventCompleted/EventStopped for the three moments the spec actually wants to hear about.
	announce := func(event string) error {
		resp, err := tc.AnnounceAll(tor.Announce, tor.AnnounceList, func(trackerURL string) (string, error) {
			left := tor.TotalLength - bytesOwned
			if left < 0 {
				left = 0
			}
			return tracker.BuildAnnounceURL(trackerURL, tor.InfoHash, peerID, opts.Port,
				progress.BytesUploaded(), progress.BytesDownloaded(), left, event)
		})
		if err != nil {
			return err
		}

		if resp.MinInterval > 0 {
			minReannounceInterval = time.Duration(resp.MinInterval) * time.Second
		}
		if resp.Interval > 0 {
			announceInterval = time.Duration(resp.Interval) * time.Second
		}

		mu.Lock()
		defer mu.Unlock()
		for _, addr := range resp.Peers {
			if connected[addr] {
				continue
			}
			select {
			case sem <- struct{}{}:
			default:
				continue // at the concurrency cap - left unconnected so a later announce can retry it
			}
			connected[addr] = true
			wg.Add(1)
			go func(addr netip.AddrPort) {
				defer wg.Done()
				defer func() { <-sem }()
				worker(ctx, addr, tor.InfoHash, peerID, pieceCount, coordinator, progress, rates, seed)
			}(addr)
		}
		return nil
	}

	if err := announce(tracker.EventStarted); err != nil {
		return fmt.Errorf("download: initial announce: %w", err)
	}
	started := true
	// A graceful exit tells the tracker we're gone - real trackers use this to drop us from the peer list
	// immediately instead of waiting out a stale entry's timeout. Best-effort: a failed stopped announce isn't
	// worth turning a clean shutdown into an error over, and there's nothing to tell the tracker if we never
	// managed to tell it we'd started in the first place.
	defer func() {
		if started {
			_ = announce(tracker.EventStopped)
		}
	}()

	lastProgress := time.Now()
	lastAnnounce := time.Now()

	progressTicker := time.NewTicker(progressLogInterval)
	defer progressTicker.Stop()

	// downloadComplete flips once, the moment the last piece lands - after that this loop keeps running as a
	// seeder (choking, serving requests, staying connected) until ctx is cancelled, instead of returning. A
	// client that exits the instant it finishes downloading can never actually seed anyone.
	downloadComplete := completed == pieceCount
	if downloadComplete {
		if !opts.Seed {
			slog.Info("download already complete from resumed data, seeding not requested (-seed=false)")
			cancel()
			wg.Wait()
			return nil
		}
		// Resume found every piece already verified on disk - there's no result left to arrive on resultCh, so
		// the transition below (which only fires when a fresh result completes the last piece) would otherwise
		// never run and this client would idle forever without ever entering seed mode.
		slog.Info("download already complete from resumed data, seeding immediately")
	}
	// linesDrawn tracks how many lines the last progress block wrote, so the next redraw knows how far to move the
	// cursor back up. It resets to 0 whenever a slog line needs to interrupt the block (completion, shutdown) so
	// that line lands on its own row instead of the next redraw overwriting it.
	linesDrawn := 0
	redraw := func(lines []string) {
		if linesDrawn > 0 {
			fmt.Fprintf(os.Stderr, "\x1b[%dA", linesDrawn)
		}
		for _, line := range lines {
			fmt.Fprintln(os.Stderr, line)
		}
		linesDrawn = len(lines)
	}
	endBlock := func() {
		linesDrawn = 0
	}

	// callerDone fires the moment callerCtx is cancelled - typically an OS signal caught in cmd/p2pget. From then
	// on this loop keeps running exactly as before for up to shutdownGracePeriod: resultCh still gets read and
	// written to disk, the bar still redraws, routine re-announces still fire. callerDone is set to nil once
	// handled so this branch never fires again (a cancelled context's Done channel stays closed forever, and a
	// nil channel is never selected) - without that, it would win the select every single time through the loop
	// from here on. The only other way ctx itself ends up cancelled inside this loop is shutdownDeadline firing
	// below, so by the time that case runs, callerCtx is always the reason - callerCtx.Err() is what a caller
	// that cancelled it is expecting back, not ctx.Err() (always context.Canceled, since this function calls its
	// own cancel() rather than being told to stop by its own parent).
	var shutdownDeadline <-chan time.Time
	callerDone := callerCtx.Done()

	for {
		select {
		case <-callerDone:
			callerDone = nil
			shutdownDeadline = time.After(shutdownGracePeriod)
			slog.Info("shutting down, giving in-flight pieces a moment to finish", "grace_period", shutdownGracePeriod)

		case <-shutdownDeadline:
			cancel() // grace period's up - force-close every connection and stop the coordinator now

		case <-ctx.Done():
			endBlock()
			wg.Wait()

			if err := sf.Sync(); err != nil {
				slog.Warn("download: sync on shutdown failed", "error", err)
			}
			slog.Info("shutdown complete",
				"pieces_done", fmt.Sprintf("%d/%d", completed, pieceCount),
				"bytes_downloaded", progress.BytesDownloaded(),
			)

			if !downloadComplete {
				return callerCtx.Err()
			}
			return nil

		case <-progressTicker.C:
			if !downloadComplete {
				redraw(progress.Render(tor.TotalLength, false))

				// Checked here, on the same 1-second ticker, rather than a separate time.After(stallTimeout) case: a
				// fresh time.After call re-armed on every trip around this select would never survive the 30 seconds it
				// needs to fire, because this ticker case wins the select every second and restarts the loop first. A
				// goroutine per connected peer stuck requesting a piece nobody in the swarm has, or every worker having
				// died and left nothing behind, is fixed the same way: more peers. Respect the tracker's own minimum
				// interval so a stall doesn't turn into a rate-limit ban.
				stalled := time.Since(lastProgress) >= stallTimeout
				allowedToReannounce := time.Since(lastAnnounce) >= minReannounceInterval
				if stalled && allowedToReannounce {
					_ = announce(tracker.EventNone) // best-effort - a failed re-announce just means we try again at the next tick
					lastAnnounce = time.Now()
				}
			} else {
				redraw(progress.Render(tor.TotalLength, true))
			}

			// Routine re-announce at the tracker's own suggested cadence, regardless of download or seed phase -
			// a pure seeder (e.g. resumed from already-complete data) otherwise sends exactly one "started"
			// announce for its entire run and then goes permanently invisible to the tracker the moment that
			// entry expires or the tracker restarts, which defeats the whole point of seeding: nobody new can
			// ever find it.
			if time.Since(lastAnnounce) >= announceInterval {
				_ = announce(tracker.EventNone) // best-effort - a failed re-announce just means we try again at the next tick
				lastAnnounce = time.Now()
			}

		case result := <-resultCh:
			if err := sf.WritePiece(result.Index, pieceCount, tor.PieceLength, tor.TotalLength, result.Data); err != nil {
				return fmt.Errorf("download: write piece %d: %w", result.Index, err)
			}
			have.Set(result.Index)
			completed++
			bytesOwned += int64(len(result.Data))
			progress.PieceCompleted()
			lastProgress = time.Now()

			if completed == pieceCount && !downloadComplete {
				downloadComplete = true
				endBlock()
				if err := sf.Sync(); err != nil {
					return fmt.Errorf("download: sync output file: %w", err)
				}

				// Best-effort, like every other announce: a tracker that gets this drops us onto its seeder
				// list, which is how new leechers find us. Without it, nothing outside this process can tell
				// seeding has even started.
				if err := announce(tracker.EventCompleted); err != nil {
					slog.Warn("download: completed announce failed", "error", err)
				}
				lastAnnounce = time.Now()

				elapsed := time.Since(start)
				attempts, successes := progress.ConnectStats()
				var successRate float64
				if attempts > 0 {
					successRate = 100 * float64(successes) / float64(attempts)
				}
				slog.Info("download complete",
					"elapsed", elapsed.Round(time.Second),
					// progress.BytesDownloaded(), not tor.TotalLength - a resumed download's elapsed time only covers
					// the bytes actually fetched this session, not the whole file, so dividing by the full size
					// would overstate throughput.
					"avg_throughput_kib_s", fmt.Sprintf("%.1f", float64(progress.BytesDownloaded())/elapsed.Seconds()/1024),
					"peak_peers", progress.PeakPeers(),
					"connect_success_rate", fmt.Sprintf("%.0f%% (%d/%d)", successRate, successes, attempts),
					"hash_failures", progress.HashFailures(),
					"panics_recovered", progress.Panics(),
					"duplicate_assignments", progress.DuplicateAssignments(),
				)

				if !opts.Seed {
					slog.Info("download complete, seeding not requested (-seed=false), shutting down")
					cancel()
					wg.Wait()
					return nil
				}
				slog.Info("seeding: download complete, continuing to serve peers until stopped")
			}
		}
	}
}
