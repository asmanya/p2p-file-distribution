package peer

import (
	"bufio"
	"net"
)

// Conn wraps a handshaken peer connection: buffered I/O, identity, and per-connection protocol state. Owned by exactly
// one goroutine - the buffered reader is not concurrent-safe.
type Conn struct {
	conn   net.Conn
	reader *bufio.Reader

	PeerID   [20]byte
	Reserved [8]byte

	AmChoking      bool // we are choking them
	AmInterested   bool // we are interested in them
	PeerChoking    bool // they are choking us
	PeerInterested bool // they are interested in us

	PeerBitfield Bitfield
}

// newConn wraps an already-handshaken net.Conn.
func newConn(conn net.Conn, peerID [20]byte, reserved [8]byte) *Conn {
	return &Conn{
		conn:        conn,
		reader:      bufio.NewReader(conn),
		PeerID:      peerID,
		Reserved:    reserved,
		AmChoking:   true, // both sides start choked/not-interested per spec
		PeerChoking: true,
	}
}
