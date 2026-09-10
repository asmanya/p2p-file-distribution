package download

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"sync"
	"time"

	"github.com/asmanya/p2p-file-distribution/internal/metainfo"
	"github.com/asmanya/p2p-file-distribution/internal/peer"
	"github.com/asmanya/p2p-file-distribution/internal/storage"
	"github.com/asmanya/p2p-file-distribution/internal/tracker"
)

// progressLogInterval is how often Download logs a progress line while running. This is the only place progress is
// printed - workers never print directly (see worker.go).
const progressLogInterval = 1 * time.Second

// listenPort is what we advertise to the tracker. This client doesn't actually accept incoming connections yet
// (that's seeding) - the value only matters for filling in the announce URL.
const listenPort = 6881

// stallTimeout is how long the download can go without a single piece completing before it's considered stalled and
// worth re-announcing to the tracker for a fresh peer list.
const stallTimeout = 30 * time.Second

// HaveBitfield tracks which pieces are already verified and on disk - the source of truth this client can seed from
// (phase 10) and what resume uses to skip pieces it doesn't need to re-download. It's written from Download's main
// goroutine as pieces complete, and will be read from other goroutines once seeding exists - a mutex is simplest and this
// struct is neither large nor hot enough to need anything fancier.
type HaveBitfield struct {
	mu sync.Mutex
	bf peer.Bitfield
}

func NewHaveBitfield(pieceCount int) *HaveBitfield {
	return &HaveBitfield{bf: make(peer.Bitfield, (pieceCount+7)/8)}
}

func (h *HaveBitfield) Set(index int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.bf.SetPiece(index)
}

func (h *HaveBitfield) Has(index int) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.bf.HasPiece(index)
}

// Download concurrently downloads every piece of tor, one worker goroutine per connected peer, and writes each verified
// piece straight to outputPath at its correct offset. It announces to tc once up front, and again - no more often than the
// tracker's own minimum interval allows - whenever no piece has completed for stallTimeout, so a torrent doesn't get
// stuck forever on whatever peers happened to be in the first response.
//
// Before requesting anything, it verifies whatever data already exists at outputPath and skips pieces that already match
// their expected hash - this is the entire resume mechanism, with no separate metadata file.
func Download(ctx context.Context, tor *metainfo.Torrent, tc *tracker.Client, peerID [20]byte, outputPath string) error {
	start := time.Now()
	pieceCount := tor.PieceCount()
	progress := NewProgress(pieceCount)

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

	resultCh := make(chan Result, resultsBufferSize)
	coordinator := NewCoordinator(pieceCount, tor.PieceLength, tor.TotalLength, tor.PiecesHashes, resultCh)

	// already-verified pieces are marked complete before Run starts - nothing to "skip" at request time, the
	// coordinator simply never offers them to anyone
	for i := 0; i < pieceCount; i++ {
		if verifiedPieces[i] {
			coordinator.MarkComplete(i)
			have.Set(i)
			completed++
			progress.PieceCompleted() // so Percent()/ETA() reflect resumed progress from the start, not just this session's
		}
	}

	if completed > 0 {
		slog.Info("resume: found existing verified data", "pieces_already_done", completed, "pieces_remaining", pieceCount-completed)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer tc.Close()

	// calling workers
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		coordinator.Run(ctx)
	}()

	var mu sync.Mutex // guards connected - touched by both announce() and the stall check
	connected := make(map[netip.AddrPort]bool)
	minReannounceInterval := stallTimeout

	// announce asks the tracker for peers and starts a worker for each one, we haven't already connected to.
	// Safe to call more than once.
	announce := func() error {
		resp, err := tc.AnnounceAll(tor.Announce, tor.AnnounceList, func(trackerURL string) (string, error) {
			return tracker.BuildAnnounceURL(trackerURL, tor.InfoHash, peerID, listenPort, tor.TotalLength)
		})
		if err != nil {
			return err
		}

		if resp.MinInterval > 0 {
			minReannounceInterval = time.Duration(resp.MinInterval) * time.Second
		}

		mu.Lock()
		defer mu.Unlock()
		for _, addr := range resp.Peers {
			if connected[addr] {
				continue
			}
			connected[addr] = true
			wg.Add(1)
			go func(addr netip.AddrPort) {
				defer wg.Done()
				worker(ctx, addr, tor.InfoHash, peerID, pieceCount, coordinator, progress)
			}(addr)
		}
		return nil
	}

	if err := announce(); err != nil {
		return fmt.Errorf("download: initial announce: %w", err)
	}

	lastProgress := time.Now()
	lastAnnounce := time.Now()

	progressTicker := time.NewTicker(progressLogInterval)
	defer progressTicker.Stop()

	for completed < pieceCount {
		select {
		case <-ctx.Done():
			cancel()
			wg.Wait()
			return ctx.Err()

		case <-progressTicker.C:
			slog.Info("download progress",
				"percent", fmt.Sprintf("%.1f%%", progress.Percent()),
				"pieces", fmt.Sprintf("%d/%d", completed, pieceCount),
				"rate_kib_s", fmt.Sprintf("%.1f", progress.Rate()/1024),
				"peers", progress.ActivePeers(),
				"eta", progress.ETA(tor.TotalLength).Round(time.Second),
			)

		case result := <-resultCh:
			if err := sf.WritePiece(result.Index, pieceCount, tor.PieceLength, tor.TotalLength, result.Data); err != nil {
				return fmt.Errorf("download: write piece %d: %w", result.Index, err)
			}
			have.Set(result.Index)
			completed++
			progress.PieceCompleted()
			lastProgress = time.Now()

		case <-time.After(stallTimeout):
			// A goroutine per connected peer is stuck requesting a piece nobody in the swarm has, or every worker
			// has died and left the queue untouched - either way, more peers are the fix. Respect the tracker's own
			// minimum interval so a stall doesn't turn into a rate-limit ban.
			stalled := time.Since(lastProgress) >= stallTimeout
			allowedToReannounce := time.Since(lastAnnounce) >= minReannounceInterval
			if stalled && allowedToReannounce {
				_ = announce() // best-effort - a failed re-announce just means we try again at the next stall check
				lastAnnounce = time.Now()
			}
		}
	}

	cancel()  // belt-and-braces: unblocks anything still mid-operation
	wg.Wait() // don't return until worker has actually cleaned up

	if err := sf.Sync(); err != nil {
		return fmt.Errorf("download: sync output file: %w", err)
	}

	elapsed := time.Since(start)
	attempts, successes := progress.ConnectStats()
	var successRate float64
	if attempts > 0 {
		successRate = 100 * float64(successes) / float64(attempts)
	}
	slog.Info("download complete",
		"elapsed", elapsed.Round(time.Second),
		// progress.BytesDownloaded(), not tor.TotalLength - a resumed download's elapsed time only covers the bytes
		// actually fetched this session, not the whole file, so dividing by the full size would overstate throughput.
		"avg_throughput_kib_s", fmt.Sprintf("%.1f", float64(progress.BytesDownloaded())/elapsed.Seconds()/1024),
		"peak_peers", progress.PeakPeers(),
		"connect_success_rate", fmt.Sprintf("%.0f%% (%d/%d)", successRate, successes, attempts),
		"hash_failures", progress.HashFailures(),
		"panics_recovered", progress.Panics(),
	)

	return nil
}
