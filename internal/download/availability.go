package download

import (
	"github.com/asmanya/p2p-file-distribution/internal/peer"
)

// Availability tracking- how many connected peers have each piece. This is the rarity signal rarest-first selection
// is built on: the fewer peers hold a piece, the more urgent it is to grab and replicate, because that piece is closer
// to vanishing from the swarm entirely if its last holder leaves.

// incAvailability records one more peer having index.
func (c *Coordinator) incAvailability(index int) {
	c.availability[index]++
}

// decAvailability records one fewer peer having index. Called for every piece a peer had when it leaves - skipping
// this for even one piece means that piece's count only ever grows, and rarest-first silently stops meaning anyhting
// for it.
func (c *Coordinator) decAvailability(index int) {
	c.availability[index]--
}

// addAvailability increments every piece present in bf - used when a peer's full bitfield arrives.
func (c *Coordinator) addAvailability(bf peer.Bitfield) {
	for i := 0; i < c.pieceCount; i++ {
		if bf.HasPiece(i) {
			c.incAvailability(i)
		}
	}
}

// removeAvailability decrements every piece present in bf - used when a peer leaves.
func (c *Coordinator) removeAvailability(bf peer.Bitfield) {
	for i := 0; i < c.pieceCount; i++ {
		if bf.HasPiece(i) {
			c.decAvailability(i)
		}
	}
}
