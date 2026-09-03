package peer

import (
	"encoding/binary"
	"fmt"
	"io"
)

// MessageID identifies a peer wire message's type
type MessageID int8

const (
	MsgKeepAlive     MessageID = -1 // sentinel: does not exist on the wire, length-0 messages map here
	MsgChoke         MessageID = 0
	MsgUnchoke       MessageID = 1
	MsgInterested    MessageID = 2
	MsgNotInterested MessageID = 3
	MsgHave          MessageID = 4
	MsgBitfield      MessageID = 5
	MsgRequest       MessageID = 6
	MsgPiece         MessageID = 7
	MsgCancel        MessageID = 8
	MsgPort          MessageID = 9
)

// String returns a human-readable name for logging
func (id MessageID) String() string {
	switch id {
	case MsgKeepAlive:
		return "keep-alive"
	case MsgChoke:
		return "choke"
	case MsgUnchoke:
		return "unchoke"
	case MsgInterested:
		return "interested"
	case MsgNotInterested:
		return "not interested"
	case MsgHave:
		return "have"
	case MsgBitfield:
		return "bitfield"
	case MsgRequest:
		return "request"
	case MsgPiece:
		return "piece"
	case MsgCancel:
		return "cancel"
	case MsgPort:
		return "port"
	default:
		return fmt.Sprintf("unknown(%d)", id)
	}
}

// Message is a single peer wire message: an ID plus its raw payload bytes.
// A keep-alive has ID MsgKeepAlive and a nil/empty Payload.
type Message struct {
	ID      MessageID
	Payload []byte
}

// ReadMessage reads and returns the next message from r.
//
// Wire layout: [4 bytes length][1 byte ID][payload...]. length counts the
// ID byte plus the payload, so it's always >= 1 for a real message.
//
// Example - a "have" message announcing piece index 5:
//
//	00 00 00 05   04            00 00 00 05
//	└─ length=5 ┘ └ ID=4(have)┘ └─ payload: piece index 5 ┘
//
// A keep-alive is just length=0, with no ID byte and no payload at all:
//
//	00 00 00 00
func ReadMessage(r io.Reader) (Message, error) {
	// The length prefix is always exactly 4 bytes - read it with a plain
	// array, not a slice, since its size never varies.
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return Message{}, fmt.Errorf("peer: read message length: %w", err)
	}
	length := binary.BigEndian.Uint32(lenBuf[:])

	// length == 0 means keep-alive: no ID byte follows on the wire at all.
	if length == 0 {
		return Message{ID: MsgKeepAlive}, nil
	}

	// Guard before allocating - a malicious/buggy peer could otherwise
	// claim a multi-gigabyte length and exhaust memory with one message.
	if length > MaxMessageLength {
		return Message{}, fmt.Errorf("peer: message length %d exceeds cap %d", length, MaxMessageLength)
	}

	// buf holds ID + payload together: buf[0] is the ID, buf[1:] is
	// whatever payload bytes remain (possibly none, e.g. choke/unchoke).
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return Message{}, fmt.Errorf("peer: read message body: %w", err)
	}

	return Message{ID: MessageID(buf[0]), Payload: buf[1:]}, nil
}

// Serialize encodes m into the wire format: [4 bytes length][1 byte ID][payload...]. A keep-alive (ID == MsgKeepAlive)
// becomes just the 4-byte zero length, with no ID byte or payload at all.
func (m Message) Serialize() []byte {
	if m.ID == MsgKeepAlive {
		return make([]byte, 4) // 00 00 00 00
	}

	length := 1 + len(m.Payload) // ID byte + payload
	buf := make([]byte, 4+length)
	binary.BigEndian.PutUint32(buf[0:4], uint32(length))
	buf[4] = byte(m.ID)
	copy(buf[5:], m.Payload)

	return buf
}
