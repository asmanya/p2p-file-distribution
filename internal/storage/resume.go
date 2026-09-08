package storage

import (
	"os"
	"runtime"
	"sync"

	"github.com/asmanya/p2p-file-distribution/internal/piece"
)

// VerifyExisting scans path (if it exists and is the expected size) and returns which pieces already match their expected
// hash. A false entry means the piece is missing, corrupt, or the file wasn't there at all - all three need the same response
// (download it), so nothing here distinguishes them.
//
// The scan is the entire resume mechanism: there's no separate metadata file recording completion, so there's nothing that can
// drift out of sync with the data on disk. What passed verification, the data itself proves.
func VerifyExisting(path string, pieceCount int, pieceLength, totalLength int64, hashes [][20]byte) ([]bool, error) {
	verified := make([]bool, pieceCount)

	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return verified, nil // nothing on disk yet - evry piece is missing
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() != totalLength {
		return verified, nil // not the file we expect - safest to treat as if nothing exists
	}

	// Bounded parallelism: one goroutine per piece would be thousands of concurrent reads on a large torrent, turnig disk
	// I/O into a random-access storm for no benefit. A worker per CPU core keeps the disk and the SHA-1 computation both
	// busy without that.
	sem := make(chan struct{}, runtime.NumCPU())
	var wg sync.WaitGroup

	for i := 0; i < pieceCount; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()

			start, end, err := piece.Range(i, pieceCount, pieceLength, totalLength)
			if err != nil {
				return
			}
			buf := make([]byte, end-start)
			if _, err := f.ReadAt(buf, start); err != nil {
				return
			}
			ok, err := piece.Verify(buf, hashes[i][:])
			if err == nil && ok {
				// Each goroutine only ever writes its own index - no two goroutines ever touch the same slot, so this needs
				// no lock despite running across many goroutines.
				verified[i] = true
			}
		}(i)
	}
	wg.Wait()

	return verified, nil
}
