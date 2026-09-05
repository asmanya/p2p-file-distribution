package peer

import (
	"encoding/binary"
	"time"
)

// writeTimeout bounds every send helper below. A peer whose receive buffer is full will block our write forever
// without a deadline.
const writeTimeout = 5 * time.Second

func (c *Conn) send(m Message) error {
	if err := c.SetIODeadline(writeTimeout); err != nil {
		return err
	}
	return c.SendMessage(m)
}

// SendInterested tells the peer we want to download from this.
func (c *Conn) SendInterested() error {
	return c.send(Message{ID: MsgInterested})
}

// SendNotInterested tells the peer we no longer want to download from them.
func (c *Conn) SendNotInterested() error {
	return c.send(Message{ID: MsgNotInterested})
}

// SendChoke tells the peer we're refusing to upload to them.
func (c *Conn) SendChoke() error {
	return c.send(Message{ID: MsgChoke})
}

// SendUnchoke tells the peer we're willing to upload to them.
func (c *Conn) SendUnchoke() error {
	return c.send(Message{ID: MsgUnchoke})
}

// SendHave announces that we've finished downloading and verified piece index.
func (c *Conn) SendHave(index int) error {
	payload := make([]byte, 4)
	binary.BigEndian.PutUint32(payload, uint32(index))
	return c.send(Message{ID: MsgHave, Payload: payload})
}

// SendRequest asks the peer for a block: piece index, byte offset within that piece, and how many bytes.
func (c *Conn) SendRequest(index, begin, length int) error {
	payload := make([]byte, 12)
	binary.BigEndian.PutUint32(payload[0:4], uint32(index))
	binary.BigEndian.PutUint32(payload[4:8], uint32(begin))
	binary.BigEndian.PutUint32(payload[8:12], uint32(length))
	return c.send(Message{ID: MsgRequest, Payload: payload})
}
