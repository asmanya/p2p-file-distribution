package peer

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// pieceMessagePayload builds a realistic MsgPiece payload: a 16 KiB block, the standard request size, prefixed
// with its 4-byte index and 4-byte begin offset - exactly what Conn.SendPiece constructs on every real block
// transfer, and the largest, most frequent message on the wire once a download or seed is actually moving data.
func pieceMessagePayload() []byte {
	payload := make([]byte, 8+BlockSize)
	binary.BigEndian.PutUint32(payload[0:4], 42)
	binary.BigEndian.PutUint32(payload[4:8], 0)
	return payload
}

// BenchmarkMessageSerialize measures the sending side of the hot path.
func BenchmarkMessageSerialize(b *testing.B) {
	msg := Message{ID: MsgPiece, Payload: pieceMessagePayload()}
	b.SetBytes(int64(5 + len(msg.Payload)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = msg.Serialize()
	}
}

// BenchmarkReadMessage measures the receiving side of the same hot path.
func BenchmarkReadMessage(b *testing.B) {
	wire := (Message{ID: MsgPiece, Payload: pieceMessagePayload()}).Serialize()
	b.SetBytes(int64(len(wire)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if _, err := ReadMessage(bytes.NewReader(wire)); err != nil {
			b.Fatalf("ReadMessage: %v", err)
		}
	}
}
