package download

import (
	"context"
	"log/slog"
	"net/netip"
	"time"

	"github.com/asmanya/p2p-file-distribution/internal/peer"
)

// coordinatorTickInterval is how often Run wakes up on its own, independent of incoming events, for stall detection and
// stats work added in later steps.
const coordinatorTickInterval = 1 * time.Second

// pieceState is the coordinator's own view of one piece, independent of any single peer's bitfield.
type pieceState int

const (
	pieceMissing pieceState = iota
	pieceInFlight
	pieceComplete
)

// peerInfo is everything the coordinator remembers about one connected peer.
type peerInfo struct {
	commands chan<- Command
	have     peer.Bitfield
	assigned int // piece index this peer is currently downloading, -1 if idle

	interested bool // true once the peer has told us (via MsgInterested) it wants to download from us
	choked     bool // our own record of whether we're choking this peer - Conn.AmChoking lives in the worker goroutine and
	// isn't safe to read here, so the coordinator keeps its own copy

	peerChoking bool // whether they're choking us - starts true, per the protocol's own default
	gotBitfield bool // whether their bitfield has already been counted into availability
}

// assignment records one peer currently downloading one piece, and when that assignment was made. Timestamps are
// per-assignee, not per-piece: during endgame a piece can have several assignees who started at different times,
// and a slow first assignee timing out must not take down a healthy later one.
type assignment struct {
	addr netip.AddrPort
	at   time.Time
}

// Coordinator owns every piece of state a Design B download needs: which pieces are done, how rare each one is, which peers
// are connected, and what's currently in flight. All of it lives inside Run's goroutine and is reached only through events
// - nothing here is guarded by a mutex, because no other goroutine ever touches it.
type Coordinator struct {
	pieceCount               int
	pieceLength, totalLength int64
	pieceHashes              [][20]byte

	pieces       []pieceState
	availability []int
	peers        map[netip.AddrPort]*peerInfo
	assignments  map[int][]assignment // piece index -> current assignees (usually 1, more only during endgame)
	endgame      bool

	events  chan Event
	results chan<- Result

	progress *Progress
	rates    *RateTracker

	optimisticAddr netip.AddrPort // which peer currently holds the optimistic-unchoke slot
	hasOptimistic  bool           // whether optimisticAddr is actually valid right now
}

// NewCoordinator builds a coordinator ready to Run. results is where completed pieces get handed off for disk writes - the
// coordinator itself never touches disk. progress may be nil - every Progress method is a safe no-op on a nil receiver.
func NewCoordinator(pieceCount int, pieceLength, totalLength int64, pieceHashes [][20]byte, results chan<- Result, progress *Progress, rates *RateTracker) *Coordinator {
	return &Coordinator{
		pieceCount:   pieceCount,
		pieceLength:  pieceLength,
		totalLength:  totalLength,
		pieceHashes:  pieceHashes,
		pieces:       make([]pieceState, pieceCount),
		availability: make([]int, pieceCount),
		peers:        make(map[netip.AddrPort]*peerInfo),
		assignments:  make(map[int][]assignment),
		events:       make(chan Event),
		results:      results,
		progress:     progress,
		rates:        rates,
	}
}

// Events return the channel workers report events on. Send-only, so callers can't accidentally read from it.
func (c *Coordinator) Events() chan<- Event {
	return c.events
}

// Run is the coordinator's single goroutine - the only place this struct's state is ever read or written. It blocks until
// ctx is cancelled.
func (c *Coordinator) Run(ctx context.Context) {
	ticker := time.NewTicker(coordinatorTickInterval)
	defer ticker.Stop()
	chokeTicker := time.NewTicker(chokeInterval)
	defer chokeTicker.Stop()
	optimisticTicker := time.NewTicker(optimisticInterval)
	defer optimisticTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-c.events:
			c.handle(ctx, ev)
		case <-ticker.C:
			c.tick()
		case <-chokeTicker.C:
			c.recalcChoke()
		case <-optimisticTicker.C:
			c.rotateOptimistic()
		}
	}
}

func (c *Coordinator) handle(ctx context.Context, ev Event) {
	switch e := ev.(type) {
	case PeerJoined:
		c.handlePeerJoined(e)
	case BitfieldReceived:
		c.handleBitfieldReceived(e)
	case HaveReceived:
		c.handleHaveReceived(e)
	case PieceDownloaded:
		c.handlePieceDownloaded(ctx, e)
	case PieceFailed:
		c.handlePieceFailed(e)
	case PeerReady:
		c.handlePeerReady(e)
	case PeerLeft:
		c.handlePeerLeft(e)
	case InterestedReceived:
		c.handleInterestedReceived(e)
	case NotInterestedReceived:
		c.handleNotInterestedReceived(e)
	case ChokeReceived:
		c.handleChokeReceived(e)
	case UnchokeReceived:
		c.handleUnchokeReceived(e)
	}
}

func (c *Coordinator) handlePeerJoined(e PeerJoined) {
	// Two live connections can end up sharing an address (a peer that dialed us from the same port we dialed
	// it on, say). Overwriting the entry would strand the old one's availability count and let its eventual
	// PeerLeft subtract pieces the new connection never added, so retire the old one properly first.
	if _, exists := c.peers[e.Addr]; exists {
		c.handlePeerLeft(PeerLeft{Addr: e.Addr})
	}
	c.peers[e.Addr] = &peerInfo{
		commands:    e.Commands,
		have:        make(peer.Bitfield, (c.pieceCount+7)/8),
		assigned:    -1,
		choked:      true,
		peerChoking: true,
	}
}

func (c *Coordinator) handleBitfieldReceived(e BitfieldReceived) {
	p, ok := c.peers[e.Addr]
	if !ok {
		return
	}
	if p.gotBitfield {
		return // a second bitfield would count every one of its pieces into availability twice
	}
	p.gotBitfield = true
	p.have = e.Bitfield
	c.addAvailability(p.have)
	c.tryAssign(e.Addr, p)
}

func (c *Coordinator) handleHaveReceived(e HaveReceived) {
	p, ok := c.peers[e.Addr]
	if !ok {
		return
	}
	if p.have.HasPiece(e.Index) {
		return // redundant have - already counted, don't inflate availability
	}
	p.have.SetPiece(e.Index)
	c.incAvailability(e.Index)
	c.tryAssign(e.Addr, p)
}

func (c *Coordinator) handleChokeReceived(e ChokeReceived) {
	if p, ok := c.peers[e.Addr]; ok {
		p.peerChoking = true
	}
}

func (c *Coordinator) handleUnchokeReceived(e UnchokeReceived) {
	p, ok := c.peers[e.Addr]
	if !ok {
		return
	}
	p.peerChoking = false
	c.tryAssign(e.Addr, p) // the first moment this peer can actually be given work
}

func (c *Coordinator) handlePieceDownloaded(ctx context.Context, e PieceDownloaded) {
	if c.pieces[e.Index] == pieceComplete {
		return // already completed by someone else after this one timed out - ignore the late duplicate
	}
	c.pieces[e.Index] = pieceComplete
	c.cancelOtherAssignees(e.Index, e.Addr)
	delete(c.assignments, e.Index)
	if p, ok := c.peers[e.Addr]; ok {
		p.assigned = -1
	}

	select {
	case c.results <- Result{Index: e.Index, Data: e.Data}:
	case <-ctx.Done():
	}
}

func (c *Coordinator) handlePieceFailed(e PieceFailed) {
	slog.Warn("coordinator: piece failed, reassigning", "peer", e.Addr, "index", e.Index, "error", e.Reason)
	c.removeAssignee(e.Index, e.Addr)
	if len(c.assignments[e.Index]) == 0 {
		c.pieces[e.Index] = pieceMissing
	}
	if p, ok := c.peers[e.Addr]; ok {
		p.assigned = -1
	}
}

func (c *Coordinator) handlePeerReady(e PeerReady) {
	if p, ok := c.peers[e.Addr]; ok {
		c.tryAssign(e.Addr, p)
	}
}

// tryAssign gives p something to download if it's idle, unchoked, and holds a piece worth having. Every path
// that can make one of those three things newly true funnels through here, so no single missed event can leave a
// peer parked forever.
func (c *Coordinator) tryAssign(addr netip.AddrPort, p *peerInfo) {
	if p.assigned >= 0 {
		return // already working on something - a second assignment would double-book it
	}
	if p.peerChoking {
		return // requests would go unanswered, and the piece would sit reserved until it timed out
	}
	if c.endgame {
		c.assignAnyMissingTo(addr, p)
		return
	}
	index, found := c.selectPieceFor(p.have)
	if !found {
		return // nothing useful for this peer right now
	}
	c.assignPiece(addr, p, index)
}

// assignIdlePeers is the safety net behind every event-driven assignment path: once a second, any peer that
// could be working but isn't gets another look. Without it, a peer that was idle at the one moment it asked for
// work - because every piece it holds was in flight elsewhere - would stay idle indefinitely, since nothing
// re-offers work when those pieces later come free.
func (c *Coordinator) assignIdlePeers() {
	for addr, p := range c.peers {
		c.tryAssign(addr, p)
	}
}

func (c *Coordinator) handlePeerLeft(e PeerLeft) {
	p, ok := c.peers[e.Addr]
	if !ok {
		return
	}
	c.removeAvailability(p.have)

	if p.assigned >= 0 {
		c.removeAssignee(p.assigned, e.Addr)
		if len(c.assignments[p.assigned]) == 0 {
			c.pieces[p.assigned] = pieceMissing
		}
	}
	c.rates.Remove(e.Addr)
	if c.hasOptimistic && c.optimisticAddr == e.Addr {
		c.hasOptimistic = false
	}
	delete(c.peers, e.Addr)
}

func (c *Coordinator) tick() {
	c.freeStaleAssignments()
	c.maybeEnterEndgame()
	c.assignIdlePeers()
}

// removeAssignee drops addr from index's assignee list, if present - used whenever a peer stops downloading a piece,
// for any reason (fail, leave, timeout, cancelled).
func (c *Coordinator) removeAssignee(index int, addr netip.AddrPort) {
	assignees := c.assignments[index]
	for i, a := range assignees {
		if a.addr == addr {
			c.assignments[index] = append(assignees[:i], assignees[i+1:]...)
			return
		}
	}
}

// MarkComplete marks index as already downloaded and verified - used once at startup for pieces resume found already correct on
// disk. Only safe to call before Run starts: there's no event/channel path for it because it only ever runs before the single-owner
// goroutine exists to race against.
func (c *Coordinator) MarkComplete(index int) {
	c.pieces[index] = pieceComplete
}
