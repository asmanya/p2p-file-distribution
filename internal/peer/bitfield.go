package peer

import "fmt"

// Bitfield represents which piece a peer has, one bit per piece. Piece 0 is the MSB of the first byte, piece 7
// is the LSB of the first byte, peice 8 the MSB of the second byte, and so on.
type Bitfield []byte

// HasPiece reports whether index's bit is set.
func (bf Bitfield) HasPiece(index int) bool {
	byteIndex := index / 8
	bitOffset := 7 - (index % 8) // 0 = MSB, 7 = LSB

	if byteIndex < 0 || byteIndex >= len(bf) {
		return false
	}

	return ((bf[byteIndex] >> bitOffset) & 1) != 0
}

// SetPiece sets index's bit.
func (bf Bitfield) SetPiece(index int) {
	byteIndex := index / 8
	bitOffset := 7 - (index % 8)

	if byteIndex < 0 || byteIndex >= len(bf) {
		return
	}

	bf[byteIndex] |= 1 << bitOffset
}

// Count returns how many pieces are marked present.
func (bf Bitfield) Count() int {
	n := 0
	for _, b := range bf {
		for i := 0; i < 8; i++ {
			if b>>i&1 != 0 {
				n++
			}
		}
	}
	return n
}

// Validate reports whether bf's length is consistent with pieceCount - it must have exactly enough bytes to hold
// pieceCount bits, no fewer, no more.
func Validate(bf Bitfield, pieceCount int) error {
	want := (pieceCount + 7) / 8
	if len(bf) != want {
		return fmt.Errorf("peer: bitfield length %d, want %d for %d pieces", len(bf), want, pieceCount)
	}
	return nil
}
