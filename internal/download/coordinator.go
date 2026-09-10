package download

import (
	"context"
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
}

// Coordinator owns every piece of state a Design B download needs: which pieces are done, how rare each one is, which peers
// are connected, dnd what's currently in flight. All of it lives inside Run's goroutine and is reached only through events
// - nothing here is guarded by a mutex, becuase no other goroutine ever touches it.
type Coordinator struct {
	pieceCount               int
	pieceLength, totalLength int64
	pieceHashes              [][20]byte

	pieces       []pieceState
	availability []int
	peers        map[netip.AddrPort]*peerInfo
	assignments  map[int][]netip.AddrPort // piece index -> holder, in flight pieces only
	assignedAt   map[int]time.Time        // piece index -> when it was assigned, for timeout detection
	endgame      bool

	events  chan Event
	results chan<- Result

	progress *Progress
}

// NewCoordinator builds a coordinator ready to Run. results is where completed pieces get handed off for disk writes - the
// coordinator itself never touches disk. progress may be nil - every Progress method is a safe no-op on a nil receiver.
func NewCoordinator(pieceCount int, pieceLength, totalLength int64, pieceHashes [][20]byte, results chan<- Result, progress *Progress) *Coordinator {
	return &Coordinator{
		pieceCount:   pieceCount,
		pieceLength:  pieceLength,
		totalLength:  totalLength,
		pieceHashes:  pieceHashes,
		pieces:       make([]pieceState, pieceCount),
		availability: make([]int, pieceCount),
		peers:        make(map[netip.AddrPort]*peerInfo),
		assignments:  make(map[int][]netip.AddrPort),
		events:       make(chan Event),
		results:      results,
		assignedAt:   make(map[int]time.Time),
		progress:     progress,
	}
}

// Events return the channel workers report events on. Send-only, so callers can't accidently read from it.
func (c *Coordinator) Events() chan<- Event {
	return c.events
}

// Run is the coordinator's single goroutine - the only place this struct's state is ever read or written. It blocks until
// ctx is cancelled.
func (c *Coordinator) Run(ctx context.Context) {
	ticker := time.NewTicker(coordinatorTickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-c.events:
			c.handle(ctx, ev)
		case <-ticker.C:
			c.tick()
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
	}
}

func (c *Coordinator) handlePeerJoined(e PeerJoined) {
	c.peers[e.Addr] = &peerInfo{
		commands: e.Commands,
		have:     make(peer.Bitfield, (c.pieceCount+7)/8),
		assigned: -1,
	}
}

func (c *Coordinator) handleBitfieldReceived(e BitfieldReceived) {
	p, ok := c.peers[e.Addr]
	if !ok {
		return
	}
	p.have = e.Bitfield
	c.addAvailability(p.have)
}

func (c *Coordinator) handleHaveReceived(e HaveReceived) {
	p, ok := c.peers[e.Addr]
	if !ok {
		return
	}
	if p.have.HasPiece(e.Index) {
		return // redundant have - already counted, dont inflate availability
	}
	p.have.SetPiece(e.Index)
	c.incAvailability(e.Index)
}

func (c *Coordinator) handlePieceDownloaded(ctx context.Context, e PieceDownloaded) {
	if c.pieces[e.Index] == pieceComplete {
		return // already completed by someone else after this one timed out - ignore the late duplicate
	}
	c.pieces[e.Index] = pieceComplete
	c.cancelOtherAssignees(e.Index, e.Addr)
	delete(c.assignments, e.Index)
	delete(c.assignedAt, e.Index)
	if p, ok := c.peers[e.Addr]; ok {
		p.assigned = -1
	}

	select {
	case c.results <- Result{Index: e.Index, Data: e.Data}:
	case <-ctx.Done():
	}
}

func (c *Coordinator) handlePieceFailed(e PieceFailed) {
	c.removeAssignee(e.Index, e.Addr)
	if len(c.assignments[e.Index]) == 0 {
		c.pieces[e.Index] = pieceMissing
		delete(c.assignedAt, e.Index)
	}
	if p, ok := c.peers[e.Addr]; ok {
		p.assigned = -1
	}
}

func (c *Coordinator) handlePeerReady(e PeerReady) {
	p, ok := c.peers[e.Addr]
	if !ok {
		return
	}
	if c.endgame {
		c.assignAnyMissingTo(e.Addr, p)
		return
	}
	index, found := c.selectPieceFor(p.have)
	if !found {
		return // nothign useful for this peer right now
	}
	c.AssignPiece(e.Addr, p, index)
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
			delete(c.assignedAt, p.assigned)
		}
	}
	delete(c.peers, e.Addr)
}

func (c *Coordinator) tick() {
	c.freeStaleAssignments()
	c.maybeEnterEndgame()
}

// removeAssignee drops addr from index's assignee list, if present - used whenever a peer stops downloading a piece,
// for any reason (fail, leave, timeout, cancelled).
func (c *Coordinator) removeAssignee(index int, addr netip.AddrPort) {
	assignees := c.assignments[index]
	for i, a := range assignees {
		if a == addr {
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
