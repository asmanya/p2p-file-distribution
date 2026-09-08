package storage

import (
	"crypto/sha1"
	"testing"

	"github.com/asmanya/p2p-file-distribution/internal/piece"
)

// testFixture builds deterministic test data and its piece hashes entirely in memory - storage's tests don't need a
// real .torrent file, just consistent geometry and hashes to verify writes and resume against. pieceLength (16) and
// totalLength (70) are chosen so the file has four full pieces and one short last piece (70 = 4*16 + 6), the same
// edge case every other layer's geometry tests cover.
func testFixture(t *testing.T) (data []byte, pieceCount int, pieceLength, totalLength int64, hashes [][20]byte) {
	t.Helper()
	pieceLength = 16
	totalLength = 70
	data = make([]byte, totalLength)
	for i := range data {
		data[i] = byte(i)
	}

	pieceCount = int((totalLength + pieceLength - 1) / pieceLength)
	hashes = make([][20]byte, pieceCount)
	for i := 0; i < pieceCount; i++ {
		start, end, err := piece.Range(i, pieceCount, pieceLength, totalLength)
		if err != nil {
			t.Fatalf("piece.Range: %v", err)
		}
		hashes[i] = sha1.Sum(data[start:end])
	}
	return data, pieceCount, pieceLength, totalLength, hashes
}
