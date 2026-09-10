package download

import (
	"log/slog"
	"math/rand/v2"
	"net/netip"
	"time"

	"github.com/asmanya/p2p-file-distribution/internal/peer"
	"github.com/asmanya/p2p-file-distribution/internal/piece"
)

// assignmentTimeout is how long a piece stays in-flight before the coordinator gives up on the peer it assigned to and
// frees the piece for someone else. Without this, a half-open TCP connection - one where the peer is gone but OS hasn't
// noticed yet - would hold a piece hostage forever, and the download would stall at 99& with no error and no log to explain why.
const assignmentTimeout = 30 * time.Second

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

// sendAssignCommand sends an AssignPiece command for index to p and records the in-flight bookkeeping. Returns false
// without touching state if p's command channel is full - a slow or dead peer must never stall the coordinator.
func (c *Coordinator) sendAssignCommand(addr netip.AddrPort, p *peerInfo, index int) bool {
	length, err := piece.Length(index, c.pieceCount, c.pieceLength, c.totalLength)
	if err != nil {
		return false // can't happen for a valid index, but never worth a coordinator panic over
	}

	select {
	case p.commands <- AssignPiece{Index: index, Length: length, ExpectedHash: c.pieceHashes[index][:]}:
	default:
		return false
	}

	c.pieces[index] = pieceInFlight
	c.assignments[index] = append(c.assignments[index], addr)
	if _, ok := c.assignedAt[index]; !ok {
		c.assignedAt[index] = time.Now()
	}

	slog.Debug("coordinator: assigned piece",
		"index", index,
		"peer", addr,
		"availability", c.availability[index],
		"endgame", c.endgame,
	)
	return true
}

// assignpiece is the normal (non-endgame) path: exactly one assignment per piece.
func (c *Coordinator) AssignPiece(addr netip.AddrPort, p *peerInfo, index int) bool {
	if !c.sendAssignCommand(addr, p, index) {
		return false
	}
	p.assigned = index
	return true
}

// freeStaleAssignments releases any piece that's been in flight longer than assignmentTimeout, marking it missing again
// so the next ready peer can pick it up. Called from Coordinator's periodic tick, not from any single event.
func (c *Coordinator) freeStaleAssignments() {
	now := time.Now()
	for index, assignedAt := range c.assignedAt {
		if now.Sub(assignedAt) < assignmentTimeout {
			continue
		}
		for _, addr := range c.assignments[index] {
			if p, ok := c.peers[addr]; ok && p.assigned == index {
				p.assigned = -1
			}
		}
		c.pieces[index] = pieceMissing
		delete(c.assignments, index)
		delete(c.assignedAt, index)
	}
}
