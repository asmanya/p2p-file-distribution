package download

import (
	"math/rand/v2"
	"net/netip"

	"github.com/asmanya/p2p-file-distribution/internal/peer"
	"github.com/asmanya/p2p-file-distribution/internal/piece"
)

// selectPieceFor picks the best piece to assign a peer with the given bitfield: among pieces that are (a) missing,
// (b) not already in flight, and (c) present in have, it picks the one with the lowest availability - the piece closest
// to disappearing from the swarm entirely if its last holder leaves. Ties are broken randomly (reservoir sampling of one)
// so peers don't all converge on  the same single rarest piece and serialize the download.
//
// Returns false if the peer has nothing useful to offer right now.
func (c *Coordinator) selectPieceFor(have peer.Bitfield) (int, bool) {
	best := -1
	bestAvailability := 0
	ties := 0

	for i := 0; i < c.pieceCount; i++ {
		if c.pieces[i] != pieceMissing || !have.HasPiece(i) {
			continue
		}
		switch {
		case best == -1, c.availability[i] < bestAvailability:
			best = i
			bestAvailability = c.availability[i]
			ties = 1

		case c.availability[i] == bestAvailability:
			ties++
			if rand.IntN(ties) == 0 {
				best = i
			}
		}
	}

	return best, best != -1
}

// assignPiece marks index in-flight to addr and sends it a commadn to start downloading. If the peer's command channel
// is full, it gives up rather than blocking - a slow or dead peer must never stall the coordinator, and the piece
// stays missing for someone else to pick up.
func (c *Coordinator) AssignPiece(addr netip.AddrPort, p *peerInfo, index int) bool {
	length, err := piece.Length(index, c.pieceCount, c.pieceLength, c.totalLength)
	if err != nil {
		return false // can't happen for a valid index, but never worth a coordinator panic over
	}

	select {
	case p.commands <- AssignPiece{Index: index, Length: length}:
	default:
		return false
	}

	c.pieces[index] = pieceInFlight
	c.assignments[index] = addr
	p.assigned = index
	return true
}
