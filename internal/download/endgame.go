package download

import "net/netip"

// endgameThreshold is how many pieces must remain missing before endgame mode kicks in - ow enough that assigning every
// remaining piece to every peer that has it only costs a small, bounded amount of duplicate bandwidth, high enough to
// actually eliminate the "last piece stuck on one slow peer" tail latency problem.
const endgameThreshold = 10

// missingPieceCount scans piece state - fine here since maybeEnterEndgame only runs once per tick, not once per assignment
func (c *Coordinator) missingPieceCount() int {
	n := 0
	for _, s := range c.pieces {
		if s == pieceMissing {
			n++
		}
	}
	return n
}

// maybeEnterEndgame flips the coordinator into endgame mode once few enough pieces remian missing. Endgame never turns
// back off - by the time it triggers, the download is nearly done anyway.
func (c *Coordinator) maybeEnterEndgame() {
	if c.endgame {
		return
	}
	missing := c.missingPieceCount()
	if missing == 0 || missing > endgameThreshold {
		return
	}
	c.endgame = true
	for addr, p := range c.peers {
		c.assignAnyMissingTo(addr, p)
	}
}

// assignAnyMissingTo gives an idle peer p one still-incomplete piece it has, ignoring whether some other peer is
// already downloading it - endgame turns duplicate prevention off deliberately, so a piece already in flight to
// someone else is still a valid target (only a piece that's actually done is excluded). A no-op if p is busy or has
// none of the remaining pieces; called again the moment p becomes ready.
func (c *Coordinator) assignAnyMissingTo(addr netip.AddrPort, p *peerInfo) {
	if p.assigned >= 0 {
		return
	}
	for index, state := range c.pieces {
		if state == pieceComplete || !p.have.HasPiece(index) {
			continue
		}
		if c.sendAssignCommand(addr, p, index) {
			p.assigned = index
		}
		return
	}
}

// cancelOtherAssignees tells every peer assigned to index, other than winner, to stop - endgame's duplicate requests only
// make sense until the first one actually finishes. Sending the cancel isn't optional: a peer left sending blocks nobody
// needs any more wastes the swarm's bandwidth, not just this client's.
func (c *Coordinator) cancelOtherAssignees(index int, winner netip.AddrPort) {
	for _, addr := range c.assignments[index] {
		if addr == winner {
			continue
		}
		p, ok := c.peers[addr]
		if !ok {
			continue
		}
		select {
		case p.commands <- CancelPiece{Index: index}:
		default:
			// channel full - the peer finds out this piece is done some other way (finishes and gets the pieceComplete guard,
			// or its own state catches up later.)
		}
		if p.assigned == index {
			p.assigned = -1
		}
	}
}
