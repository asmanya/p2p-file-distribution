package download

import (
	"net/netip"
	"testing"
	"time"
)

// bumpDownloadRate/bumpUploadRate directly record two byte samples for addr, spread over a real backdated
// interval, so DownloadRate/UploadRate come back proportional to bytesPerSecond instead of the near-zero-elapsed
// rate two back-to-back real-time samples would produce - same trick as rates_test.go.
func bumpDownloadRate(rt *RateTracker, addr netip.AddrPort, bytesPerSecond int) {
	rt.AddDownloaded(addr, bytesPerSecond)
	rt.mu.Lock()
	rt.peers[addr].downSamples[0].at = time.Now().Add(-time.Second)
	rt.mu.Unlock()
	rt.AddDownloaded(addr, bytesPerSecond)
}

func bumpUploadRate(rt *RateTracker, addr netip.AddrPort, bytesPerSecond int) {
	rt.AddUploaded(addr, bytesPerSecond)
	rt.mu.Lock()
	rt.peers[addr].upSamples[0].at = time.Now().Add(-time.Second)
	rt.mu.Unlock()
	rt.AddUploaded(addr, bytesPerSecond)
}

// --- Scenario 1: tit-for-tat unchokes the fastest interested peers -------
func TestCoordinatorRecalcChokeUnchokesTopRatePeers(t *testing.T) {
	// Two pieces: one we already have, one still missing. Peers only hold the piece we have, so there's nothing
	// to assign (no AssignPiece noise on the command channels) while the download is still officially
	// incomplete - which is what keeps this on the download-rate branch rather than the seeder one.
	c, _ := newTestCoordinator(2)
	c.MarkComplete(0)
	base := netip.MustParseAddr("127.0.0.1")

	var addrs []netip.AddrPort
	var commands []chan Command
	for i := 0; i < 6; i++ {
		addr := netip.AddrPortFrom(base, uint16(i+1))
		addrs = append(addrs, addr)
		commands = append(commands, joinPeer(c, addr, bitfieldOf(2, 0)))
		c.handleInterestedReceived(InterestedReceived{Addr: addr})
		bumpDownloadRate(c.rates, addr, (i+1)*1000)
	}

	c.recalcChoke()

	for i, addr := range addrs {
		wantUnchoked := i >= 2 // indices 2..5 hold the top 4 download rates
		select {
		case cmd := <-commands[i]:
			if _, ok := cmd.(UnchokePeer); !ok {
				t.Errorf("peer %d (%v) got %T, want UnchokePeer", i, addr, cmd)
			}
			if !wantUnchoked {
				t.Errorf("peer %d (%v) should not have been unchoked (rate too low)", i, addr)
			}
		default:
			if wantUnchoked {
				t.Errorf("peer %d (%v) expected UnchokePeer, got nothing", i, addr)
			}
		}
	}
}

// --- Scenario 2: losing interest chokes immediately, not at the next recalc ---
func TestCoordinatorNotInterestedChokesImmediately(t *testing.T) {
	c, _ := newTestCoordinator(1)
	c.MarkComplete(0)
	addr := netip.MustParseAddrPort("127.0.0.1:1")
	commands := joinPeer(c, addr, bitfieldOf(1, 0))

	c.handleInterestedReceived(InterestedReceived{Addr: addr})
	bumpDownloadRate(c.rates, addr, 1000)
	c.recalcChoke()
	<-commands // drain the UnchokePeer recalcChoke just sent

	c.handleNotInterestedReceived(NotInterestedReceived{Addr: addr})

	select {
	case cmd := <-commands:
		if _, ok := cmd.(ChokePeer); !ok {
			t.Fatalf("got %T, want ChokePeer", cmd)
		}
	default:
		t.Fatal("expected an immediate ChokePeer after NotInterestedReceived")
	}
}

// --- Scenario 3: optimistic unchoke reaches a peer tit-for-tat never would ---
func TestCoordinatorRotateOptimisticUnchokesAChokedInterestedPeer(t *testing.T) {
	c, _ := newTestCoordinator(1)
	c.MarkComplete(0)
	addr := netip.MustParseAddrPort("127.0.0.1:1")
	commands := joinPeer(c, addr, bitfieldOf(1, 0))
	c.handleInterestedReceived(InterestedReceived{Addr: addr}) // no rate bumped - the regular algorithm alone would never unchoke this peer

	c.rotateOptimistic()

	select {
	case cmd := <-commands:
		if _, ok := cmd.(UnchokePeer); !ok {
			t.Fatalf("got %T, want UnchokePeer", cmd)
		}
	default:
		t.Fatal("expected the optimistic slot to unchoke the only interested peer")
	}
	if !c.hasOptimistic || c.optimisticAddr != addr {
		t.Errorf("hasOptimistic=%v optimisticAddr=%v, want true/%v", c.hasOptimistic, c.optimisticAddr, addr)
	}
}

// --- Scenario 4: rotating releases the old slot and picks a new one -------
func TestCoordinatorRotateOptimisticReleasesPreviousAndPicksNew(t *testing.T) {
	c, _ := newTestCoordinator(1)
	c.MarkComplete(0)
	addrA := netip.MustParseAddrPort("127.0.0.1:1")
	commandsA := joinPeer(c, addrA, bitfieldOf(1, 0))
	c.handleInterestedReceived(InterestedReceived{Addr: addrA})

	c.rotateOptimistic() // only candidate - must pick A
	<-commandsA          // drain A's UnchokePeer

	addrB := netip.MustParseAddrPort("127.0.0.1:2")
	commandsB := joinPeer(c, addrB, bitfieldOf(1, 0))
	c.handleInterestedReceived(InterestedReceived{Addr: addrB})

	c.rotateOptimistic() // A is now "previous" and excluded, B is the only other candidate - deterministic

	select {
	case cmd := <-commandsA:
		if _, ok := cmd.(ChokePeer); !ok {
			t.Fatalf("A got %T, want ChokePeer (releasing the old optimistic slot)", cmd)
		}
	default:
		t.Fatal("expected A's optimistic slot to be released")
	}
	select {
	case cmd := <-commandsB:
		if _, ok := cmd.(UnchokePeer); !ok {
			t.Fatalf("B got %T, want UnchokePeer", cmd)
		}
	default:
		t.Fatal("expected B to receive the new optimistic slot")
	}
	if c.optimisticAddr != addrB {
		t.Errorf("optimisticAddr = %v, want %v", c.optimisticAddr, addrB)
	}
}

// --- Scenario 5: seeder mode sorts by upload rate, not download rate -----
func TestCoordinatorRecalcChokeSortsByUploadRateWhenSeeding(t *testing.T) {
	c, _ := newTestCoordinator(1)
	c.MarkComplete(0) // remainingPieceCount() == 0 -> seeding mode

	base := netip.MustParseAddr("127.0.0.1")
	var addrs []netip.AddrPort
	var commands []chan Command
	for i := 0; i < 6; i++ {
		addr := netip.AddrPortFrom(base, uint16(i+1))
		addrs = append(addrs, addr)
		commands = append(commands, joinPeer(c, addr, bitfieldOf(1, 0)))
		c.handleInterestedReceived(InterestedReceived{Addr: addr})
		// Download rate is deliberately the reverse ranking of upload rate - if recalcChoke used download rate
		// while seeding, this test would unchoke the wrong four peers.
		bumpDownloadRate(c.rates, addr, (6-i)*1000)
		bumpUploadRate(c.rates, addr, (i+1)*1000)
	}

	c.recalcChoke()

	for i, addr := range addrs {
		wantUnchoked := i >= 2 // indices 2..5 hold the top 4 upload rates
		select {
		case cmd := <-commands[i]:
			if _, ok := cmd.(UnchokePeer); !ok {
				t.Errorf("peer %d (%v) got %T, want UnchokePeer", i, addr, cmd)
			}
			if !wantUnchoked {
				t.Errorf("peer %d (%v) should not have been unchoked (upload rate too low)", i, addr)
			}
		default:
			if wantUnchoked {
				t.Errorf("peer %d (%v) expected UnchokePeer, got nothing", i, addr)
			}
		}
	}
}
