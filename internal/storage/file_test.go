package storage

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/asmanya/p2p-file-distribution/internal/piece"
)

// --- Case 1: pieces written out of order -----------------------------------
//
// A concurrent download finishes pieces in whatever order peers happen to answer, not index order. This proves
// positional writes make that irrelevant: every piece lands at its correct offset regardless of write order.
func TestWritePieceOutOfOrder(t *testing.T) {
	data, pieceCount, pieceLength, totalLength, _ := testFixture(t)
	path := filepath.Join(t.TempDir(), "out.dat")

	sf, err := Create(path, totalLength)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer sf.Close()

	// Deliberately not 0,1,2,3,4 - the whole point is that write order must not matter.
	order := []int{2, 0, 4, 1, 3}
	for _, i := range order {
		start, end, err := piece.Range(i, pieceCount, pieceLength, totalLength)
		if err != nil {
			t.Fatalf("piece.Range(%d): %v", i, err)
		}
		if err := sf.WritePiece(i, pieceCount, pieceLength, totalLength, data[start:end]); err != nil {
			t.Fatalf("WritePiece(%d): %v", i, err)
		}
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Error("final file bytes don't match expected data")
	}
}

// --- Case 2: the final, shorter piece ---------------------------------------
//
// testFixture's total length isn't an exact multiple of the piece length, so the last piece is short. This checks
// that writing it doesn't leave the file longer than totalLength (no accidental padding to a full piece's worth of
// bytes) and that reading it back gives exactly the short piece's own bytes, not a full piece's length.
func TestWritePieceLastShortPiece(t *testing.T) {
	data, pieceCount, pieceLength, totalLength, _ := testFixture(t)
	path := filepath.Join(t.TempDir(), "out.dat")

	sf, err := Create(path, totalLength)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer sf.Close()

	lastIndex := pieceCount - 1
	start, end, err := piece.Range(lastIndex, pieceCount, pieceLength, totalLength)
	if err != nil {
		t.Fatalf("piece.Range: %v", err)
	}
	if err := sf.WritePiece(lastIndex, pieceCount, pieceLength, totalLength, data[start:end]); err != nil {
		t.Fatalf("WritePiece: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Size() != totalLength {
		t.Errorf("file size %d, want %d (no extra padding from the short last piece)", info.Size(), totalLength)
	}

	got, err := sf.ReadPiece(lastIndex, pieceCount, pieceLength, totalLength)
	if err != nil {
		t.Fatalf("ReadPiece: %v", err)
	}
	if !bytes.Equal(got, data[start:end]) {
		t.Error("last piece's bytes don't match expected data")
	}
}

// --- Case 3: many goroutines writing different pieces at once ---------------
//
// This is the actual concurrency claim storage.go's doc comment makes: positional writes to non-overlapping byte
// ranges need no lock, since the ranges never overlap and *os.File's WriteAt is safe to call concurrently. Run
// under `-race`, this is the test that would catch it if that claim were ever wrong.
func TestWritePieceConcurrentDifferentIndices(t *testing.T) {
	data, pieceCount, pieceLength, totalLength, _ := testFixture(t)
	path := filepath.Join(t.TempDir(), "out.dat")

	sf, err := Create(path, totalLength)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer sf.Close()

	// One goroutine per piece, all writing at once - every goroutine touches a different, non-overlapping byte
	// range, so nothing here needs a mutex.
	var wg sync.WaitGroup
	for i := 0; i < pieceCount; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			start, end, err := piece.Range(i, pieceCount, pieceLength, totalLength)
			if err != nil {
				t.Errorf("piece.Range(%d): %v", i, err)
				return
			}
			if err := sf.WritePiece(i, pieceCount, pieceLength, totalLength, data[start:end]); err != nil {
				t.Errorf("WritePiece(%d): %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Error("final file bytes don't match expected data")
	}
}
