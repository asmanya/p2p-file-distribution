package peer

import (
	"errors"
	"fmt"
	"net"
	"time"
)

const dialTimeout = 5 * time.Second // cap on establishing the TCP connection itself

// handshakeTimeout is a var, not a const, so tests can shrink it temporarily
// instead of waiting out the real 5s deadline.
var handshakeTimeout = 5 * time.Second // cap on the whole handshake exchange, once connected

var (
	ErrHandshakeTimeout = errors.New("peer: handshake timeout")
	ErrProtocolMismatch = errors.New("peer: protocol mismatch")
	ErrInfoHashMismatch = errors.New("peer: info hash mismatch")
)

// Conn wraps an established, handshaken connection to a peer.
type Conn struct {
	conn     net.Conn
	PeerID   [20]byte
	Reserved [8]byte
}

// Dial connects to addr, exchanges handshakes, and verifies infoHash matches.
func Dial(addr string, infoHash, peerID [20]byte) (*Conn, error) {
	// Bound how long establishing the TCP connection itself can take.
	conn, err := net.DialTimeout("tcp", addr, dialTimeout)
	if err != nil {
		return nil, fmt.Errorf("peer: dial %s: %w", addr, err)
	}

	return handshakeOver(conn, addr, infoHash, peerID)
}

// connects to addr, exchanges handshakes, and verifies infoHash matches without tcp.
func handshakeOver(conn net.Conn, addr string, infoHash, peerID [20]byte) (*Conn, error) {
	// One deadline covers both the write and the read below - a peer that
	// accepts the connection and then does nothing must not hang forever.
	if err := conn.SetDeadline(time.Now().Add(handshakeTimeout)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("peer: set deadline: %w", err)
	}

	// Send our handshake first.
	ours := Handshake{InfoHash: infoHash, PeerID: peerID}
	if _, err := conn.Write(ours.Serialize()); err != nil {
		conn.Close()
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return nil, fmt.Errorf("peer: %s: %w", addr, ErrHandshakeTimeout)
		}

		return nil, fmt.Errorf("peer: send handshake: %w", err)
	}

	// Then read theirs.
	theirs, err := ParseHandshake(conn)
	if err != nil {
		conn.Close()
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return nil, fmt.Errorf("peer: %s: %w", addr, ErrHandshakeTimeout)
		}

		return nil, fmt.Errorf("peer: receive handshake: %w", err)
	}

	// A correct handshake from the wrong torrent is still a failure.
	if theirs.InfoHash != infoHash {
		conn.Close()
		return nil, fmt.Errorf("peer: %s: %w", addr, ErrInfoHashMismatch)
	}

	// Handshake is done - clear the deadline so it can't fire mid-download
	// later. Per-operation deadlines come back in a later phase.
	if err := conn.SetDeadline(time.Time{}); err != nil {
		conn.Close()
		return nil, fmt.Errorf("peer: clear deadline: %w", err)
	}

	return &Conn{conn: conn, PeerID: theirs.PeerID, Reserved: theirs.Reserved}, nil
}
