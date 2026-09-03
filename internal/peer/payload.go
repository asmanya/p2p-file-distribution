package peer

import (
	"encoding/binary"
	"fmt"
)

// HavePayload holds just the piece index a have message announces - nothing else.
type HavePayload struct {
	Index int // which piece the peer now has
}

// ParseHavePayload validates and parses a have message's payload.
func ParseHavePayload(payload []byte, pieceCount int) (HavePayload, error) {
	if len(payload) != 4 {
		return HavePayload{}, fmt.Errorf("peer: have payload length %d, want 4", len(payload))
	}
	index := int(binary.BigEndian.Uint32(payload))
	if index < 0 || index >= pieceCount {
		return HavePayload{}, fmt.Errorf("peer: have piece index %d out of range [0,%d)", index, pieceCount)
	}

	return HavePayload{Index: index}, nil
}

// RequestPayload identifies a block: which piece, what bytes offset within it, and how many bytes.
// Used by both request and cancel messages.
type RequestPayload struct {
	Index  int // which piece
	Begin  int // byte offset within that piece
	Length int // how many bytes wanted, starting at Begin
}

// ParseRequestPayload validates and parses a request/cancel message's payload.
func ParseRequestPayload(payload []byte, pieceCount int, pieceLength int) (RequestPayload, error) {
	if len(payload) != 12 {
		return RequestPayload{}, fmt.Errorf("peer: request payload length %d, want 12", len(payload))
	}

	index := int(binary.BigEndian.Uint32(payload[0:4]))
	begin := int(binary.BigEndian.Uint32(payload[4:8]))
	length := int(binary.BigEndian.Uint32(payload[8:12]))

	if index < 0 || index >= pieceCount {
		return RequestPayload{}, fmt.Errorf("peer: request piece index %d out of range [0,%d)", index, pieceCount)
	}
	if begin < 0 || begin >= pieceLength {
		return RequestPayload{}, fmt.Errorf("peer request begin %d out of range [0,%d)", begin, pieceLength)
	}
	if begin+length > pieceLength {
		return RequestPayload{}, fmt.Errorf("peer: request block [%d,%d) exceeds piece length %d", begin, begin+length, pieceLength)
	}

	return RequestPayload{Index: index, Begin: begin, Length: length}, nil
}

// PiecePayload is a block of actual piece data: which piece, what byte offset within it, and the block bytes themselves.
type PiecePayload struct {
	Index int    // which piece
	Begin int    // byte offset within that piece where Block starts
	Block []byte // the actual downloaded file bytes
}

// ParsePiecePayload validates and parses a piece message's payload.
func ParsePiecePayload(payload []byte, pieceCount int, pieceLength int) (PiecePayload, error) {
	if len(payload) < 8 {
		return PiecePayload{}, fmt.Errorf("peer: piece payload length %d, want at least 8", len(payload))
	}

	index := int(binary.BigEndian.Uint32(payload[0:4]))
	begin := int(binary.BigEndian.Uint32(payload[4:8]))
	block := payload[8:]

	if index < 0 || index >= pieceCount {
		return PiecePayload{}, fmt.Errorf("peer: piece index %d out of range [0,%d)", index, pieceCount)
	}
	if begin < 0 || begin >= pieceLength {
		return PiecePayload{}, fmt.Errorf("peer: piece begin %d out of range [0,%d)", begin, pieceLength)
	}
	if begin+len(block) > pieceLength {
		return PiecePayload{}, fmt.Errorf("peer: piece block [%d,%d) exceeds piece length %d", begin, begin+len(block), pieceLength)
	}

	return PiecePayload{Index: index, Begin: begin, Block: block}, nil
}

// PortPayload is the listen port announced by a port message (BEP-5 DHT). This client doesn't implement DHT, so the value is parsed but unused.
type PortPayload struct {
	Port uint16 // peer's DHT listen port
}

// ParsePortPayload validates and parses a port message's payload
func ParsePortPayload(payload []byte) (PortPayload, error) {
	if len(payload) != 2 {
		return PortPayload{}, fmt.Errorf("peer: port payload length %d, want 2", len(payload))
	}
	return PortPayload{Port: binary.BigEndian.Uint16(payload)}, nil
}
