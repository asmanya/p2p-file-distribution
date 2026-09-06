package download

import (
	"context"
	"net/netip"
	"sync"

	"github.com/asmanya/p2p-file-distribution/internal/metainfo"
	"github.com/asmanya/p2p-file-distribution/internal/piece"
)

// Download concurrently downloads every piece of tor from peers, one worker goroutine per peer address, and returns
// the assembled, fully verified file contents.
//
// TODO: this holds the entire file in memory - fine for a torrent the size of a Linux ISO, but will exhaust memory
// on anything much larger. Fixed by streaming verified pieces to disk instead, once storage exists.
func Download(ctx context.Context, tor *metainfo.Torrent, peers []netip.AddrPort, peerID [20]byte) ([]byte, error) {
	pieceCount := tor.PieceCount()

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

	// calling workers
	var wg sync.WaitGroup
	for _, addr := range peers {
		wg.Add(1)
		go func(addr netip.AddrPort) {
			defer wg.Done()
			worker(ctx, addr, tor.InfoHash, peerID, pieceCount, workCh, resultCh)
		}(addr)

	}

	// joining pieces together
	buf := make([]byte, tor.TotalLength)
	for completed := 0; completed < pieceCount; completed++ {
		result := <-resultCh
		start, _, err := piece.Range(result.Index, pieceCount, tor.PieceLength, tor.TotalLength)
		if err != nil {
			return nil, err
		}
		copy(buf[start:], result.Data)
	}

	close(workCh) // no more work - remaining idle workers see this and exit
	cancel()      // belt-and-braces: unblocks anything still mid-operation
	wg.Wait()     // don't return until worker has actually cleaned up

	return buf, nil
}
