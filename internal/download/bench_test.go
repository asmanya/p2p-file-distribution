package download

import (
	"math/rand/v2"
	"testing"

	"github.com/asmanya/p2p-file-distribution/internal/peer"
)

// BenchmarkSelectPieceFor measures rarest-first selection at a piece count far larger than any real torrent this
// client has actually downloaded (Debian's netinst ISO tops out around 3,000 pieces). Step 9.5 chose a plain O(n)
// scan over a priority queue on the assumption that n stays small enough for this not to matter - this benchmark
// is what turns that assumption into a measurement instead of a guess.
func BenchmarkSelectPieceFor(b *testing.B) {
	const pieceCount = 50000
	c, _ := newTestCoordinator(pieceCount)

	have := make(peer.Bitfield, (pieceCount+7)/8)
	for i := 0; i < pieceCount; i++ {
		have.SetPiece(i)
		c.availability[i] = rand.IntN(20) // a realistic spread, not every piece tied at zero
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.selectPieceFor(have)
	}
}
