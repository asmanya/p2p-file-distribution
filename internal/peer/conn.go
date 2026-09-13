package peer

import (
	"bufio"
	"fmt"
	"net"
	"time"
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

// NewConn wraps an already-handshaken net.Conn. Exported for tests (in this
// and other packages) that simulate a peer connection - e.g. over
// net.Pipe() - without going through a real Dial/handshake. Production code
// should always obtain a *Conn via Dial instead.
func NewConn(conn net.Conn, peerID [20]byte, reserved [8]byte) *Conn {
	return newConn(conn, peerID, reserved)
}

// SetReadDeadline and SetWriteDeadline bound the next read and the next write independently. They are separate
// on purpose: a Conn is read by its own goroutine while the connection's main loop writes to it, so a single
// combined deadline would let a short write deadline silently cut short a long read already in flight - a peer
// that simply had nothing to say for a few seconds would be dropped as if it had gone silent for the full read
// timeout. Every network operation on a Conn goes through one of these; there are no deadline-free reads or
// writes.
func (c *Conn) SetReadDeadline(d time.Duration) error {
	if err := c.conn.SetReadDeadline(time.Now().Add(d)); err != nil {
		return fmt.Errorf("peer: set read deadline: %w", err)
	}
	return nil
}

// SetWriteDeadline sets a deadline d from now on the underlying connection's writes, mirroring SetReadDeadline for
// the write direction - the reader and the connection's own writer run on different goroutines sharing one
// net.Conn, so a single combined deadline would let one direction's timeout silently cut the other short.
func (c *Conn) SetWriteDeadline(d time.Duration) error {
	if err := c.conn.SetWriteDeadline(time.Now().Add(d)); err != nil {
		return fmt.Errorf("peer: set write deadline: %w", err)
	}
	return nil
}

// ReadMessage reads the next message from the connection's buffered reader.
func (c *Conn) ReadMessage() (Message, error) {
	return ReadMessage(c.reader)
}

// SendMessage serializes and writes m to the connection.
func (c *Conn) SendMessage(m Message) error {
	if _, err := c.conn.Write(m.Serialize()); err != nil {
		return fmt.Errorf("peer: write message: %w", err)
	}
	return nil
}

// Close closes the underlying connection.
func (c *Conn) Close() error {
	return c.conn.Close()
}
