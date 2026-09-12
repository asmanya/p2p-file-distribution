package download

import (
	"log/slog"
	"math/rand/v2"
	"net/netip"
	"sort"
	"time"
)

// chokeInterval and optimisticInterval are vars, not consts, so tests can shrink them instead of waiting out a
// real 10- or 30-second cycle - the same reason readTimeout and pieceTimeout are vars.
var (
	// chokeInterval is how often the tit-for-tat decision recalculates - the top unchokeSlots interested peers by
	// rate get unchoked, everyone else choked, 10 seconds matches the spec's convention.
	chokeInterval = 10 * time.Second

	// optimisticInterval is how often the optimistic-unchoke slot rotates to a new random peer, independent of the
	// regular recalc's own cycle.
	optimisticInterval = 30 * time.Second
)

// unchokeSlots is how many interested peers the regular (non-optimistic) algorithm keeps unchoked at once.
const unchokeSlots = 4

func (c *Coordinator) handleInterestedReceived(e InterestedReceived) {
	if p, ok := c.peers[e.Addr]; ok {
		p.interested = true
	}
}

func (c *Coordinator) handleNotInterestedReceived(e NotInterestedReceived) {
	p, ok := c.peers[e.Addr]
	if !ok {
		return
	}
	p.interested = false
	// A peer that isn't interested gets choked right away rather than waiting for the next chokeInterval recalc - there's
	// nothing to gain from holding a slot open for a peer that won't use it.
	c.setChoked(e.Addr, p, true)
	if c.hasOptimistic && c.optimisticAddr == e.Addr {
		c.hasOptimistic = false
	}
}

// setChoked sends a choke/unchoke command only if it actually changes p's state, and updates the coordinator's own
// record to match. A full command channel is not an error - the peer's worker is just behind, and the next recalc
// will try again.
func (c *Coordinator) setChoked(addr netip.AddrPort, p *peerInfo, choked bool) {
	if p.choked == choked {
		return
	}
	var cmd Command
	if choked {
		cmd = ChokePeer{}
	} else {
		cmd = UnchokePeer{}
	}
	select {
	case p.commands <- cmd:
		p.choked = choked
		slog.Debug("coordinator: choke state changed", "peer", addr, "choked", choked)
	default:
	}
}

// recalcChoke runs the tit-for-tat decision: the unchokeSlots interested peers giving us the best rate get unchoked,
// everyone else gets choked - except whoever currently holds the optimistic slot, who stays unchoked regardless of rate
// until rotateOptimistic picks someone new.
func (c *Coordinator) recalcChoke() {
	seeding := c.remainingPieceCount() == 0

	type candidate struct {
		addr netip.AddrPort
		p    *peerInfo
		rate float64
	}
	var candidates []candidate
	for addr, p := range c.peers {
		if !p.interested {
			c.setChoked(addr, p, true)
			continue
		}
		r := c.rates.DownloadRate(addr)
		if seeding {
			// Once every piece is done, download rate from peer means nothing - upload rate is what actually reflects
			// who's worth prioritizing bandwidth to
			r = c.rates.UploadRate(addr)
		}
		candidates = append(candidates, candidate{addr: addr, p: p, rate: r})
	}

	sort.Slice(candidates, func(i, j int) bool { return candidates[i].rate > candidates[j].rate })

	unchoked := make(map[netip.AddrPort]bool, unchokeSlots+1)
	for i := 0; i < len(candidates) && i < unchokeSlots; i++ {
		unchoked[candidates[i].addr] = true
	}
	if c.hasOptimistic {
		if p, ok := c.peers[c.optimisticAddr]; ok && p.interested {
			unchoked[c.optimisticAddr] = true
		} else {
			c.hasOptimistic = false
		}
	}

	for _, cand := range candidates {
		c.setChoked(cand.addr, cand.p, !unchoked[cand.addr])
	}
}

// rotateOptimistic releases whoever currently holds the optimistic slot and picks a new random interested-but-choked
// peer to unchoke unconditionally for the next optimisticInterval - giving a peer tit-for-tat would never reach on its
// own (it has nothing to offer yet) a chance to start reciprocating. Runs on its own 30-second timer, independent of
// recalcChoke's 10-second cycle.
func (c *Coordinator) rotateOptimistic() {
	previous, hadPrevious := c.optimisticAddr, c.hasOptimistic
	if hadPrevious {
		if p, ok := c.peers[previous]; ok {
			c.setChoked(previous, p, true)
		}
	}
	c.hasOptimistic = false

	var candidates []netip.AddrPort
	for addr, p := range c.peers {
		if !p.interested || !p.choked {
			continue
		}
		if hadPrevious && addr == previous {
			continue
		}
		candidates = append(candidates, addr)
	}
	if len(candidates) == 0 {
		return
	}

	addr := candidates[rand.IntN(len(candidates))]
	c.optimisticAddr = addr
	c.hasOptimistic = true
	c.setChoked(addr, c.peers[addr], false)
}
