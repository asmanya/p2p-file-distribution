package download

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/asmanya/p2p-file-distribution/internal/peer"
)

// --- Shared helpers -----------------------------------------------------
//
// Every test below feeds the coordinator events directly and inspects its state or the commands it sends back -
// no network, no peers, no timing. That's the whole point of Step 9.2's event types: the coordinator is a pure
// state machine, so it's testable in milliseconds instead of needing fake seeders and real timeouts.

// newTestCoordinator builds a Coordinator with pieceCount pieces of length 1 each and no real hashes - these tests
// care about piece indices and coordinator state, never about geometry or actual verification.
func newTestCoordinator(pieceCount int) (*Coordinator, chan Result) {
	results := make(chan Result, pieceCount)
	return NewCoordinator(pieceCount, 1, int64(pieceCount), make([][20]byte, pieceCount), results), results
}

// bitfieldOf builds a peer.Bitfield with exactly the given piece indices set.
func bitfieldOf(pieceCount int, indices ...int) peer.Bitfield {
	bf := make(peer.Bitfield, (pieceCount+7)/8)
	for _, i := range indices {
		bf.SetPiece(i)
	}
	return bf
}

// joinPeer registers a peer and delivers its bitfield in one step - most tests below don't care about the two
// events happening separately, only about the resulting state.
func joinPeer(c *Coordinator, addr netip.AddrPort, bf peer.Bitfield) chan Command {
	commands := make(chan Command, 4)
	c.handlePeerJoined(PeerJoined{Addr: addr, Commands: commands})
	c.handleBitfieldReceived(BitfieldReceived{Addr: addr, Bitfield: bf})
	return commands
}

// --- Scenario 1: one peer, one bitfield ----------------------------------
func TestCoordinatorBitfieldSetsAvailability(t *testing.T) {
	c, _ := newTestCoordinator(4)
	addr := netip.MustParseAddrPort("127.0.0.1:1")
	joinPeer(c, addr, bitfieldOf(4, 0, 1, 2))

	want := []int{1, 1, 1, 0}
	for i, w := range want {
		if c.availability[i] != w {
			t.Errorf("piece %d availability=%d, want %d", i, c.availability[i], w)
		}
	}
}

// --- Scenario 2: two peers, overlapping bitfields ------------------------
//
// Piece 0 is held by both peers (availability 2); piece 1 only by B (availability 1). B should be assigned the
// rarer piece, not the one it shares with everyone else.
func TestCoordinatorRarestFirstAssignsRarerPiece(t *testing.T) {
	c, _ := newTestCoordinator(2)
	addrA := netip.MustParseAddrPort("127.0.0.1:1")
	addrB := netip.MustParseAddrPort("127.0.0.1:2")

	joinPeer(c, addrA, bitfieldOf(2, 0))
	commandsB := joinPeer(c, addrB, bitfieldOf(2, 0, 1))

	c.handlePeerReady(PeerReady{Addr: addrB})

	select {
	case cmd := <-commandsB:
		assign, ok := cmd.(AssignPiece)
		if !ok {
			t.Fatalf("expected AssignPiece, got %T", cmd)
		}
		if assign.Index != 1 {
			t.Errorf("assigned piece %d, want the rarer piece 1", assign.Index)
		}
	default:
		t.Fatal("expected an assignment, got none")
	}
}

// --- Scenario 3: peer leaves ----------------------------------------------
func TestCoordinatorPeerLeftFreesAvailabilityAndAssignment(t *testing.T) {
	c, _ := newTestCoordinator(1)
	addr := netip.MustParseAddrPort("127.0.0.1:1")
	joinPeer(c, addr, bitfieldOf(1, 0))
	c.handlePeerReady(PeerReady{Addr: addr})

	if c.pieces[0] != pieceInFlight {
		t.Fatalf("piece 0 should be in-flight before the peer leaves, got %v", c.pieces[0])
	}

	c.handlePeerLeft(PeerLeft{Addr: addr})

	if c.availability[0] != 0 {
		t.Errorf("availability=%d after the only peer left, want 0", c.availability[0])
	}
	if c.pieces[0] != pieceMissing {
		t.Errorf("piece 0 state=%v after its holder left, want missing", c.pieces[0])
	}
	if len(c.assignments[0]) != 0 {
		t.Errorf("piece 0 still has assignees after its holder left: %v", c.assignments[0])
	}
}

// --- Scenario 4: two peers, one piece - duplicate prevention -------------
func TestCoordinatorDuplicatePreventionOnlyOneAssignee(t *testing.T) {
	c, _ := newTestCoordinator(1)
	addrA := netip.MustParseAddrPort("127.0.0.1:1")
	addrB := netip.MustParseAddrPort("127.0.0.1:2")

	joinPeer(c, addrA, bitfieldOf(1, 0))
	commandsB := joinPeer(c, addrB, bitfieldOf(1, 0))

	c.handlePeerReady(PeerReady{Addr: addrA})
	c.handlePeerReady(PeerReady{Addr: addrB})

	select {
	case cmd := <-commandsB:
		t.Fatalf("expected peer B to get nothing (piece already in flight to A), got %T", cmd)
	default:
	}

	if len(c.assignments[0]) != 1 {
		t.Errorf("piece 0 has %d assignees, want exactly 1", len(c.assignments[0]))
	}
}

// --- Scenario 5: piece fails, goes missing, is reassignable ---------------
func TestCoordinatorPieceFailedIsReassignable(t *testing.T) {
	c, _ := newTestCoordinator(1)
	addr := netip.MustParseAddrPort("127.0.0.1:1")
	commands := joinPeer(c, addr, bitfieldOf(1, 0))

	c.handlePeerReady(PeerReady{Addr: addr})
	<-commands // drain the first assignment

	c.handlePieceFailed(PieceFailed{Addr: addr, Index: 0})
	if c.pieces[0] != pieceMissing {
		t.Fatalf("piece 0 state=%v after failing, want missing", c.pieces[0])
	}

	c.handlePeerReady(PeerReady{Addr: addr})
	select {
	case cmd := <-commands:
		if _, ok := cmd.(AssignPiece); !ok {
			t.Fatalf("expected a reassignment, got %T", cmd)
		}
	default:
		t.Fatal("expected the failed piece to be reassigned, got nothing")
	}
}

// --- Scenario 6: assignment timeout frees a stale piece -------------------
func TestCoordinatorAssignmentTimeoutFreesStalePiece(t *testing.T) {
	c, _ := newTestCoordinator(1)
	addr := netip.MustParseAddrPort("127.0.0.1:1")
	joinPeer(c, addr, bitfieldOf(1, 0))
	c.handlePeerReady(PeerReady{Addr: addr})

	c.assignedAt[0] = time.Now().Add(-assignmentTimeout - time.Second)
	c.freeStaleAssignments()

	if c.pieces[0] != pieceMissing {
		t.Errorf("piece 0 state=%v after its assignment timed out, want missing", c.pieces[0])
	}
	if len(c.assignments[0]) != 0 {
		t.Errorf("piece 0 still has assignees after timeout: %v", c.assignments[0])
	}
}

// --- Scenario 7: few pieces left triggers endgame -------------------------
func TestCoordinatorEndgameTriggersAtThreshold(t *testing.T) {
	pieceCount := endgameThreshold + 2
	c, _ := newTestCoordinator(pieceCount)

	// Mark all but endgameThreshold pieces complete, so exactly endgameThreshold remain missing.
	for i := 0; i < pieceCount-endgameThreshold; i++ {
		c.MarkComplete(i)
	}

	addr := netip.MustParseAddrPort("127.0.0.1:1")
	all := make([]int, 0, pieceCount)
	for i := 0; i < pieceCount; i++ {
		all = append(all, i)
	}
	commands := joinPeer(c, addr, bitfieldOf(pieceCount, all...))

	c.maybeEnterEndgame()

	if !c.endgame {
		t.Fatal("expected endgame to have triggered")
	}
	select {
	case <-commands:
	default:
		t.Fatal("expected the idle peer to be assigned a piece once endgame triggered")
	}
}

// --- Scenario 8: endgame cancels the losers once one peer finishes -------
func TestCoordinatorEndgameCancelsOtherAssignees(t *testing.T) {
	c, resultCh := newTestCoordinator(1)
	addrA := netip.MustParseAddrPort("127.0.0.1:1")
	addrB := netip.MustParseAddrPort("127.0.0.1:2")

	peerA := joinPeer(c, addrA, bitfieldOf(1, 0))
	peerB := joinPeer(c, addrB, bitfieldOf(1, 0))
	c.endgame = true

	c.handlePeerReady(PeerReady{Addr: addrA})
	c.handlePeerReady(PeerReady{Addr: addrB})

	<-peerA // drain A's assignment
	select {
	case cmd := <-peerB:
		if _, ok := cmd.(AssignPiece); !ok {
			t.Fatalf("expected peer B to also be assigned piece 0 during endgame, got %T", cmd)
		}
	default:
		t.Fatal("expected peer B to also receive an endgame assignment")
	}

	ctx := context.Background()
	c.handlePieceDownloaded(ctx, PieceDownloaded{Addr: addrA, Index: 0, Data: []byte("x")})

	select {
	case cmd := <-peerB:
		if _, ok := cmd.(CancelPiece); !ok {
			t.Fatalf("expected peer B to receive CancelPiece, got %T", cmd)
		}
	default:
		t.Fatal("expected peer B to be cancelled after A finished first")
	}

	select {
	case <-resultCh:
	default:
		t.Fatal("expected the winning result to be forwarded for disk write")
	}
}

// --- Scenario 9: peer with nothing useful never blocks the coordinator ---
func TestCoordinatorPeerWithNoUsefulPieceDoesNotBlock(t *testing.T) {
	c, _ := newTestCoordinator(1)
	addr := netip.MustParseAddrPort("127.0.0.1:1")
	c.MarkComplete(0) // the only piece is already done - nothing left to offer
	commands := joinPeer(c, addr, bitfieldOf(1, 0))

	done := make(chan struct{})
	go func() {
		c.handlePeerReady(PeerReady{Addr: addr})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handlePeerReady blocked with nothing useful to assign")
	}

	select {
	case cmd := <-commands:
		t.Fatalf("expected no command, got %T", cmd)
	default:
	}
}
