package download

import "net/netip"

// endgameThreshold is how many pieces may remain incomplete before endgame mode kicks in - low enough that assigning
// every remaining piece to every peer that has it only costs a small, bounded amount of duplicate bandwidth, high
// enough to actually eliminate the "last piece stuck on one slow peer" tail latency problem.
const endgameThreshold = 10

// remainingPieceCount counts every piece that isn't complete yet - missing and in-flight alike. Counting only
// pieceMissing here would be wrong: a piece already assigned to someone is still outstanding work, and ignoring it
// would let endgame trigger while far more than endgameThreshold pieces are actually left, needlessly widening
// endgame's blast radius of duplicate requests. Scanning is fine since this only runs once per tick, not once per
// assignment.
func (c *Coordinator) remainingPieceCount() int {
	n := 0
	for _, s := range c.pieces {
		if s != pieceComplete {
			n++
		}
	}
	return n
}

// maybeEnterEndgame flips the coordinator into endgame mode once few enough pieces remain incomplete. Endgame never
// turns back off - by the time it triggers, the download is nearly done anyway.
func (c *Coordinator) maybeEnterEndgame() {
	if c.endgame {
		return
	}
	remaining := c.remainingPieceCount()
	if remaining == 0 || remaining > endgameThreshold {
		return
	}
	c.endgame = true
	for addr, p := range c.peers {
		c.assignAnyMissingTo(addr, p)
	}
}

// assignAnyMissingTo gives an idle peer p one still-incomplete piece it has, ignoring whether some other peer is
// already downloading it - endgame turns duplicate prevention off deliberately, so a piece already in flight to
// someone else is still a valid target (only a piece that's actually done is excluded).
//
// It picks the piece with the fewest current assignees, not simply the lowest index: without that, every idle peer
// would pick the same first incomplete piece it has, piling every peer onto one piece at a time while the rest of
// the endgame set sits completely untouched - serializing exactly the phase endgame exists to parallelize. Spreading
// idle peers across whichever remaining pieces have the least coverage keeps all of them progressing at once.
//
// A no-op if p is busy or has none of the remaining pieces; called again the moment p becomes ready.
func (c *Coordinator) assignAnyMissingTo(addr netip.AddrPort, p *peerInfo) {
	if p.assigned >= 0 {
		return
	}
	best := -1
	bestAssignees := -1
	for index, state := range c.pieces {
		if state == pieceComplete || !p.have.HasPiece(index) {
			continue
		}
		n := len(c.assignments[index])
		if best == -1 || n < bestAssignees {
			best = index
			bestAssignees = n
		}
	}
	if best == -1 {
		return
	}
	if c.sendAssignCommand(addr, p, best) {
		p.assigned = best
	}
}

// cancelOtherAssignees tells every peer assigned to index, other than winner, to stop - endgame's duplicate requests only
// make sense until the first one actually finishes. Sending the cancel isn't optional: a peer left sending blocks nobody
// needs any more wastes the swarm's bandwidth, not just this client's.
func (c *Coordinator) cancelOtherAssignees(index int, winner netip.AddrPort) {
	for _, a := range c.assignments[index] {
		if a.addr == winner {
			continue
		}
		p, ok := c.peers[a.addr]
		if !ok {
			continue
		}
		select {
		case p.commands <- CancelPiece{Index: index}:
			c.progress.DuplicateAssignment()
		default:
			// channel full - the peer finds out this piece is done some other way (finishes and gets the pieceComplete guard,
			// or its own state catches up later.)
		}
		if p.assigned == index {
			p.assigned = -1
		}
	}
}
