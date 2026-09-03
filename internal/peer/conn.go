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

// SetIODeadline sets a deadline on the underlying connection for the next
// read or write. Every network operation on a Conn must go through this -
// there are no deadline-free reads or writes.
func (c *Conn) SetIODeadline(d time.Duration) error {
	if err := c.conn.SetDeadline(time.Now().Add(d)); err != nil {
		return fmt.Errorf("peer: set deadline: %w", err)
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
