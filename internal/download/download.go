package download

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"sync"
	"time"

	"github.com/asmanya/p2p-file-distribution/internal/metainfo"
	"github.com/asmanya/p2p-file-distribution/internal/piece"
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

// Download concurrently downloads every piece of tor, one worker goroutine per connected peer, and returns
// the assembled, fully verified file contents. It announces to tc once up front, and again - no more often than the
// tracker's own minimum interval allows - whenever no piece has completed for stallTimeout, so a torrent doesn't get
// stuck forever on whatever peers happened to be in the first response.
//
// TODO: this holds the entire file in memory - fine for a torrent the size of a Linux ISO, but will exhaust memory
// on anything much larger. Fixed by streaming verified pieces to disk instead, once storage exists.
func Download(ctx context.Context, tor *metainfo.Torrent, tc *tracker.Client, peerID [20]byte) ([]byte, error) {
	start := time.Now()
	pieceCount := tor.PieceCount()
	progress := NewProgress(pieceCount)

	// creating the queue
	work := make([]piece.Work, pieceCount)
	for i := 0; i < pieceCount; i++ {
		length, err := piece.Length(i, pieceCount, tor.PieceLength, tor.TotalLength)
		if err != nil {
			return nil, err
		}
		work[i] = piece.Work{
			Index:        i,
			ExpectedHash: tor.PiecesHashes[i][:],
			Length:       length,
		}
	}

	workCh, resultCh := NewQueues(work)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer tc.Close()

	// calling workers
	var wg sync.WaitGroup
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
				worker(ctx, addr, tor.InfoHash, peerID, pieceCount, workCh, resultCh, progress)
			}(addr)
		}
		return nil
	}

	if err := announce(); err != nil {
		return nil, fmt.Errorf("download: initial announce: %w", err)
	}

	// joining pieces together
	buf := make([]byte, tor.TotalLength)
	lastProgress := time.Now()
	lastAnnounce := time.Now()

	progressTicker := time.NewTicker(progressLogInterval)
	defer progressTicker.Stop()

	for completed := 0; completed < pieceCount; {
		select {
		case <-ctx.Done():
			cancel()
			wg.Wait()
			return nil, ctx.Err()

		case <-progressTicker.C:
			slog.Info("download progress",
				"percent", fmt.Sprintf("%.1f%%", progress.Percent()),
				"pieces", fmt.Sprintf("%d/%d", completed, pieceCount),
				"rate_kib_s", fmt.Sprintf("%.1f", progress.Rate()/1024),
				"peers", progress.ActivePeers(),
				"eta", progress.ETA(tor.TotalLength).Round(time.Second),
			)

		case result := <-resultCh:
			start, _, err := piece.Range(result.Index, pieceCount, tor.PieceLength, tor.TotalLength)
			if err != nil {
				return nil, err
			}
			copy(buf[start:], result.Data)
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

	close(workCh) // no more work - remaining idle workers see this and exit
	cancel()      // belt-and-braces: unblocks anything still mid-operation
	wg.Wait()     // don't return until worker has actually cleaned up

	elapsed := time.Since(start)
	attempts, successes := progress.ConnectStats()
	var successRate float64
	if attempts > 0 {
		successRate = 100 * float64(successes) / float64(attempts)
	}
	slog.Info("download complete",
		"elapsed", elapsed.Round(time.Second),
		"avg_throughput_kib_s", fmt.Sprintf("%.1f", float64(tor.TotalLength)/elapsed.Seconds()/1024),
		"peak_peers", progress.PeakPeers(),
		"connect_success_rate", fmt.Sprintf("%.0f%% (%d/%d)", successRate, successes, attempts),
		"hash_failures", progress.HashFailures(),
		"panics_recovered", progress.Panics(),
	)

	return buf, nil
}
