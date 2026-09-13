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

// TestSync confirms Sync doesn't error on a real, open file - it's a thin wrapper over os.File.Sync, but it's the
// call every completed or gracefully-shutdown download depends on to actually get bytes onto disk.
func TestSync(t *testing.T) {
	data, pieceCount, pieceLength, totalLength, _ := testFixture(t)
	path := filepath.Join(t.TempDir(), "out.dat")

	sf, err := Create(path, totalLength)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer sf.Close()

	start, end, err := piece.Range(0, pieceCount, pieceLength, totalLength)
	if err != nil {
		t.Fatalf("piece.Range: %v", err)
	}
	if err := sf.WritePiece(0, pieceCount, pieceLength, totalLength, data[start:end]); err != nil {
		t.Fatalf("WritePiece: %v", err)
	}
	if err := sf.Sync(); err != nil {
		t.Errorf("Sync: %v", err)
	}
}

// TestReadBlock covers the seeding path: reading one block out of a piece without reading the whole piece, plus
// the bounds check that keeps a peer's request from reading past its own piece into the next one.
func TestReadBlock(t *testing.T) {
	data, pieceCount, pieceLength, totalLength, _ := testFixture(t)
	path := filepath.Join(t.TempDir(), "out.dat")

	sf, err := Create(path, totalLength)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer sf.Close()

	start, end, err := piece.Range(0, pieceCount, pieceLength, totalLength)
	if err != nil {
		t.Fatalf("piece.Range: %v", err)
	}
	pieceData := data[start:end]
	if err := sf.WritePiece(0, pieceCount, pieceLength, totalLength, pieceData); err != nil {
		t.Fatalf("WritePiece: %v", err)
	}

	t.Run("valid block", func(t *testing.T) {
		got, err := sf.ReadBlock(0, pieceCount, pieceLength, totalLength, 2, 3)
		if err != nil {
			t.Fatalf("ReadBlock: %v", err)
		}
		if !bytes.Equal(got, pieceData[2:5]) {
			t.Errorf("ReadBlock(2,3) = %v, want %v", got, pieceData[2:5])
		}
	})

	t.Run("negative begin rejected", func(t *testing.T) {
		if _, err := sf.ReadBlock(0, pieceCount, pieceLength, totalLength, -1, 3); err == nil {
			t.Error("expected an error for a negative begin, got nil")
		}
	})

	t.Run("block overrunning the piece rejected", func(t *testing.T) {
		pieceLen := end - start
		if _, err := sf.ReadBlock(0, pieceCount, pieceLength, totalLength, pieceLen-1, 10); err == nil {
			t.Error("expected an error for a block that overruns the piece, got nil")
		}
	})
}
