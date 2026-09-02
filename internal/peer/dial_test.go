package peer

import (
	"net"
	"testing"
	"time"
)

// A well-behaved fake peer that sends back a matching handshake results in a
// successful Conn with the fake peer's own peer ID recorded.
func TestHandshakeOverSuccess(t *testing.T) {
	// net.Pipe gives us two ends of an in-memory connection - no real
	// socket, no real network. "client" is our side; "server" plays the
	// fake peer in a goroutine below.
	client, server := net.Pipe()
	defer client.Close()

	infoHash := [20]byte{1, 2, 3}
	peerID := [20]byte{4, 5, 6}
	theirPeerID := [20]byte{7, 8, 9}

	// The fake peer runs on its own goroutine because net.Pipe is
	// unbuffered: a write on one end blocks until the other end reads.
	// Our handshakeOver call below writes first, so something has to be
	// reading concurrently or the whole test would deadlock.
	go func() {
		// Read (and discard) our handshake, matching what a real peer does.
		_, _ = ParseHandshake(server)
		// Reply with a valid handshake carrying the same info hash but a
		// different peer ID - that's what handshakeOver should return to us.
		server.Write((&Handshake{InfoHash: infoHash, PeerID: theirPeerID}).Serialize())
		server.Close()
	}()

	conn, err := handshakeOver(client, "test", infoHash, peerID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if conn.PeerID != theirPeerID {
		t.Fatalf("got peer ID %v, want %v", conn.PeerID, theirPeerID)
	}
}

// A peer that replies with a *valid* handshake, but for a different
// torrent, must still be rejected - a correct handshake shape says nothing
// about whether it's the right swarm.
func TestHandshakeOverInfoHashMismatch(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()

	go func() {
		_, _ = ParseHandshake(server)
		// Reply is well-formed but carries a different info hash (9,9,9)
		// than the one handshakeOver was told to expect (1,2,3) below.
		server.Write((&Handshake{InfoHash: [20]byte{9, 9, 9}}).Serialize())
		server.Close()
	}()

	_, err := handshakeOver(client, "test", [20]byte{1, 2, 3}, [20]byte{4, 5, 6})
	if err == nil {
		t.Fatal("expected info hash mismatch error")
	}
}

// A reply with the right length prefix (19) but the wrong protocol string
// text means whatever is on the other end isn't speaking BitTorrent.
func TestHandshakeOverWrongProtocolString(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()

	go func() {
		_, _ = ParseHandshake(server)
		buf := make([]byte, handshakeLen)
		buf[0] = 19
		copy(buf[1:20], "Not the right thing") // 19 bytes, but not "BitTorrent protocol"
		server.Write(buf)
		server.Close()
	}()

	_, err := handshakeOver(client, "test", [20]byte{1, 2, 3}, [20]byte{4, 5, 6})
	if err == nil {
		t.Fatal("expected protocol mismatch error")
	}
}

// A peer that sends a few bytes and then closes the connection - simulating
// a crash or a deliberately hostile peer mid-handshake - must produce a
// clean error, not a panic or a hang on io.ReadFull waiting for more bytes
// that will never come.
func TestHandshakeOverPartialThenClose(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()

	go func() {
		_, _ = ParseHandshake(server)
		server.Write([]byte{19, 'B', 'i', 't'}) // way less than the 68 bytes a handshake needs
		server.Close()
	}()

	_, err := handshakeOver(client, "test", [20]byte{1, 2, 3}, [20]byte{4, 5, 6})
	if err == nil {
		t.Fatal("expected error for partial handshake")
	}
}

// A peer that reads our handshake but never sends its own must not hang
// this program forever - the deadline handshakeOver sets has to fire.
func TestHandshakeOverNoResponse(t *testing.T) {
	// Shrink the real timeout handshakeOver uses internally, instead of
	// setting our own deadline on client - SetDeadline calls don't combine,
	// the last one set always wins, so a deadline set here would just get
	// overwritten by handshakeOver's own SetDeadline call anyway.
	old := handshakeTimeout
	handshakeTimeout = 50 * time.Millisecond
	defer func() {
		handshakeTimeout = old
	}()

	client, server := net.Pipe()
	defer server.Close()
	defer client.Close()

	go func() {
		_, _ = ParseHandshake(server) // reads our handshake, never replies
	}()

	_, err := handshakeOver(client, "test", [20]byte{1, 2, 3}, [20]byte{4, 5, 6})
	if err == nil {
		t.Fatal("expected deadline/timeout error")
	}
}

// Arbitrary bytes that happen to be 68 bytes long (or close to it) must
// still fail to parse as a handshake, not be silently accepted.
func TestHandshakeOverGarbage(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()

	go func() {
		_, _ = ParseHandshake(server)
		server.Write([]byte("total garbage, not a handshake at all"))
		server.Close()
	}()

	_, err := handshakeOver(client, "test", [20]byte{1, 2, 3}, [20]byte{4, 5, 6})
	if err == nil {
		t.Fatal("expected error for garbage response")
	}
}
