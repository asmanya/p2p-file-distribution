package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/asmanya/p2p-file-distribution/internal/piece"
)

func allTrue(bs []bool) bool {
	for _, b := range bs {
		if !b {
			return false
		}
	}
	return true
}

func allFalse(bs []bool) bool {
	for _, b := range bs {
		if b {
			return false
		}
	}
	return true
}

// --- Case 1: a complete, correct file already on disk -----------------------
//
// The simplest resume case: nothing was lost, the whole file was already downloaded in a previous run. Every piece
// should verify, which is what makes Download() queue nothing and finish immediately.
func TestVerifyExistingFullFile(t *testing.T) {
	data, pieceCount, pieceLength, totalLength, hashes := testFixture(t)
	path := filepath.Join(t.TempDir(), "out.dat")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	verified, err := VerifyExisting(path, pieceCount, pieceLength, totalLength, hashes)
	if err != nil {
		t.Fatalf("VerifyExisting: %v", err)
	}
	if !allTrue(verified) {
		t.Errorf("expected every piece verified, got %v", verified)
	}
}

// --- Case 2: an interrupted download, first half real, second half garbage --
//
// This is the realistic crash scenario: preallocation already made the file the right *size*, but only the first
// half was ever actually written - the rest is whatever the sparse file (or leftover garbage) happens to contain.
// VerifyExisting must scan every piece rather than stopping at the first failure, and report exactly which half is
// trustworthy.
func TestVerifyExistingPartialFile(t *testing.T) {
	data, pieceCount, pieceLength, totalLength, hashes := testFixture(t)
	path := filepath.Join(t.TempDir(), "out.dat")

	// Correct for the first half of pieces, garbage for the rest - same size file, so VerifyExisting still scans
	// every piece rather than bailing out early.
	corrupted := make([]byte, len(data))
	copy(corrupted, data)
	half := pieceCount / 2
	start, _, err := piece.Range(half, pieceCount, pieceLength, totalLength)
	if err != nil {
		t.Fatalf("piece.Range: %v", err)
	}
	for i := int(start); i < len(corrupted); i++ {
		corrupted[i] ^= 0xFF
	}
	if err := os.WriteFile(path, corrupted, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	verified, err := VerifyExisting(path, pieceCount, pieceLength, totalLength, hashes)
	if err != nil {
		t.Fatalf("VerifyExisting: %v", err)
	}
	for i, ok := range verified {
		want := i < half
		if ok != want {
			t.Errorf("piece %d: verified=%v, want %v", i, ok, want)
		}
	}
}

// --- Case 3: one bad piece in an otherwise-complete file ---------------------
//
// A single flipped byte inside exactly one piece - everything else is byte-perfect. This is the test that proves
// resume operates at piece granularity, not whole-file granularity: one bad piece must not throw away 4999 good
// ones on a large torrent.
func TestVerifyExistingCorruptPiece(t *testing.T) {
	data, pieceCount, pieceLength, totalLength, hashes := testFixture(t)
	path := filepath.Join(t.TempDir(), "out.dat")

	corrupted := make([]byte, len(data))
	copy(corrupted, data)
	target := pieceCount / 2
	start, _, err := piece.Range(target, pieceCount, pieceLength, totalLength)
	if err != nil {
		t.Fatalf("piece.Range: %v", err)
	}
	corrupted[start] ^= 0xFF // flip one byte inside exactly one piece
	if err := os.WriteFile(path, corrupted, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	verified, err := VerifyExisting(path, pieceCount, pieceLength, totalLength, hashes)
	if err != nil {
		t.Fatalf("VerifyExisting: %v", err)
	}
	for i, ok := range verified {
		want := i != target
		if ok != want {
			t.Errorf("piece %d: verified=%v, want %v", i, ok, want)
		}
	}
}

// --- Case 4: a brand new download, nothing on disk yet -----------------------
//
// The first-ever run of a torrent: outputPath doesn't exist at all. This must be a normal, silent "everything is
// missing" result, not an error - a missing file is the expected starting state, not a failure.
func TestVerifyExistingMissingFile(t *testing.T) {
	_, pieceCount, pieceLength, totalLength, hashes := testFixture(t)
	path := filepath.Join(t.TempDir(), "does-not-exist.dat")

	verified, err := VerifyExisting(path, pieceCount, pieceLength, totalLength, hashes)
	if err != nil {
		t.Fatalf("VerifyExisting: %v", err)
	}
	if !allFalse(verified) {
		t.Errorf("expected every piece missing, got %v", verified)
	}
}

// --- Case 5: something else's file sitting at outputPath ---------------------
//
// A file exists at the path but isn't the right size for this torrent at all - stale data from an unrelated run,
// or a user's own file that happened to collide. Trying to verify pieces against it piece-by-piece would either
// panic on out-of-range reads or silently "verify" nonsense. The safe response is the same as a missing file:
// treat everything as not-yet-downloaded, never trust a size mismatch.
func TestVerifyExistingWrongSize(t *testing.T) {
	_, pieceCount, pieceLength, totalLength, hashes := testFixture(t)
	path := filepath.Join(t.TempDir(), "out.dat")
	if err := os.WriteFile(path, []byte("way too short"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	verified, err := VerifyExisting(path, pieceCount, pieceLength, totalLength, hashes)
	if err != nil {
		t.Fatalf("VerifyExisting: %v", err)
	}
	if !allFalse(verified) {
		t.Errorf("expected every piece missing for a wrong-size file, got %v", verified)
	}
}
