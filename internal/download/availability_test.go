package download

import (
	"net/netip"
	"testing"

	"github.com/asmanya/p2p-file-distribution/internal/peer"
)

// TestAvailabilityNeverExceedsConnectedPeers : no piece's availability count can ever be higher than the number of
// currently connected peers. If a peer leaves without its pieces being decremented, this drifts upwards forever and
// rarest-first silently stops meaning anything - this test exists to catch exactly that regression.
func TestAvailabilityNeverExceedsConnectedPeers(t *testing.T) {
	const pieceCount = 10
	c := NewCoordinator(pieceCount, 1, int64(pieceCount), make([][20]byte, pieceCount), make(chan Result, pieceCount), nil, nil)

	full := make(peer.Bitfield, (pieceCount+7)/8)
	for i := 0; i < pieceCount; i++ {
		full.SetPiece(i)
	}

	addrs := []netip.AddrPort{
		netip.MustParseAddrPort("127.0.0.1:1"),
		netip.MustParseAddrPort("127.0.0.1:2"),
		netip.MustParseAddrPort("127.0.0.1:3"),
	}

	assertInvariant := func() {
		t.Helper()
		connected := len(c.peers)
		for i, n := range c.availability {
			if n > connected {
				t.Fatalf("piece %d availability=%d exceeds connected peers=%d", i, n, connected)
			}
			if n < 0 {
				t.Fatalf("piece %d availability=%d is negative", i, n)
			}
		}
	}

	// All three join with a full bitfield
	for _, a := range addrs {
		cmds := make(chan Command, 1)
		c.handlePeerJoined(PeerJoined{Addr: a, Commands: cmds})
		c.handleBitfieldReceived(BitfieldReceived{Addr: a, Bitfield: full})
		assertInvariant()
	}
	for i := 0; i < pieceCount; i++ {
		if c.availability[i] != 3 {
			t.Errorf("piece %d availability=%d, want 3", i, c.availability[i])
		}
	}

	// One leaves - every piece it had must decrement.
	c.handlePeerLeft(PeerLeft{Addr: addrs[0]})
	assertInvariant()
	for i := 0; i < pieceCount; i++ {
		if c.availability[i] != 2 {
			t.Errorf("piece %d availability=%d, want 2 after one peer left", i, c.availability[i])
		}
	}

	// The remaining two leave too - counts must return to zero, not go negative or get stuck.
	c.handlePeerLeft(PeerLeft{Addr: addrs[1]})
	c.handlePeerLeft(PeerLeft{Addr: addrs[2]})
	assertInvariant()
	for i := 0; i < pieceCount; i++ {
		if c.availability[i] != 0 {
			t.Errorf("piece %d availability=%d, want 0 after everyone left", i, c.availability[i])
		}
	}
}
