package peer

import (
	"fmt"
	"io"
)

const (
	protocolString = "BitTorrent protocol"
	handshakeLen   = 1 + len(protocolString) + 8 + 20 + 20 // = 68
)

// Handshake is the fixed 68-byte message peers exchange when a connection opens.
type Handshake struct {
	Reserved [8]byte
	InfoHash [20]byte
	PeerID   [20]byte
}

// Serialize encodes h into the exact 68-byte wire format.
func (h *Handshake) Serialize() []byte {
	buf := make([]byte, 0, handshakeLen)
	buf = append(buf, byte(len(protocolString)))
	buf = append(buf, protocolString...)
	buf = append(buf, h.Reserved[:]...)
	buf = append(buf, h.InfoHash[:]...)
	buf = append(buf, h.PeerID[:]...)
	return buf
}

// ParseHandshake reads and validates a 68-byte handshake from r.
func ParseHandshake(r io.Reader) (Handshake, error) {
	buf := make([]byte, handshakeLen)
	if _, err := io.ReadFull(r, buf); err != nil {
		return Handshake{}, fmt.Errorf("peer: read handshake: %w", err)
	}

	if buf[0] != byte(len(protocolString)) {
		return Handshake{}, fmt.Errorf("peer: invalid protocol string length %d", buf[0])
	}
	if string(buf[1:1+len(protocolString)]) != protocolString {
		return Handshake{}, fmt.Errorf("peer: unexpected protocol string %q", buf[1:1+len(protocolString)])
	}

	var h Handshake
	offset := 1 + len(protocolString)
	copy(h.Reserved[:], buf[offset:offset+8])
	offset += 8
	copy(h.InfoHash[:], buf[offset:offset+20])
	offset += 20
	copy(h.PeerID[:], buf[offset:offset+20])

	return h, nil
}
