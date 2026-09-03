package peer

import (
	"bytes"
	"testing"
)

func TestParseHavePayload(t *testing.T) {
	tests := []struct {
		name       string
		payload    []byte
		pieceCount int
		wantErr    bool
		wantIndex  int
	}{
		{"valid", []byte{0, 0, 0, 5}, 10, false, 5},              // index 5, torrent has 10 pieces -> ok
		{"wrong length", []byte{0, 0, 5}, 10, true, 0},           // only 3 bytes, have needs exactly 4
		{"index out of range", []byte{0, 0, 0, 99}, 10, true, 0}, // index 99 but only 10 pieces exist
	}

	for _, tt := range tests {
		got, err := ParseHavePayload(tt.payload, tt.pieceCount)
		if (err != nil) != tt.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", tt.name, err, tt.wantErr)
			continue
		}
		if !tt.wantErr && got.Index != tt.wantIndex {
			t.Errorf("%s: index = %d, want %d", tt.name, got.Index, tt.wantIndex)
		}
	}
}

func TestParseRequestPayload(t *testing.T) {
	const pieceCount = 10
	const pieceLength = 1000

	tests := []struct {
		name    string
		payload []byte
		wantErr bool
	}{
		{"valid", []byte{0, 0, 0, 2, 0, 0, 0, 100, 0, 0, 0, 200}, false},                     // index=2, begin=100, length=200
		{"wrong length", []byte{0, 0, 0, 2, 0, 0, 0, 100}, true},                             // only 8 bytes, request needs 12
		{"index out of range", []byte{0, 0, 0, 99, 0, 0, 0, 100, 0, 0, 0, 200}, true},        // index 99, only 10 pieces
		{"begin out of range", []byte{0, 0, 0, 2, 0, 0, 3, 232, 0, 0, 0, 200}, true},         // begin=1000 == pieceLength, out of [0,1000)
		{"block exceeds piece length", []byte{0, 0, 0, 2, 0, 0, 3, 100, 0, 0, 3, 232}, true}, // begin=868 + length=1000 > pieceLength=1000
	}

	for _, tt := range tests {
		_, err := ParseRequestPayload(tt.payload, pieceCount, pieceLength)
		if (err != nil) != tt.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", tt.name, err, tt.wantErr)
		}
	}
}

func TestParsePiecePayload(t *testing.T) {
	const pieceCount = 10
	const pieceLength = 1000

	tests := []struct {
		name    string
		payload []byte
		wantErr bool
	}{
		{"valid", append([]byte{0, 0, 0, 2, 0, 0, 0, 0}, []byte("hello")...), false},               // index=2, begin=0, block="hello"
		{"too short", []byte{0, 0, 0, 2, 0, 0}, true},                                              // only 6 bytes, need at least 8 (index+begin)
		{"index out of range", append([]byte{0, 0, 0, 99, 0, 0, 0, 0}, []byte("hello")...), true},  // index 99, only 10 pieces
		{"begin out of range", append([]byte{0, 0, 0, 2, 0, 0, 3, 232}, []byte("hello")...), true}, // begin=1000 == pieceLength
	}

	for _, tt := range tests {
		got, err := ParsePiecePayload(tt.payload, pieceCount, pieceLength)
		if (err != nil) != tt.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", tt.name, err, tt.wantErr)
			continue
		}
		if !tt.wantErr && !bytes.Equal(got.Block, []byte("hello")) {
			t.Errorf("%s: block = %q, want %q", tt.name, got.Block, "hello")
		}
	}
}

func TestParsePortPayload(t *testing.T) {
	tests := []struct {
		name     string
		payload  []byte
		wantErr  bool
		wantPort uint16
	}{
		{"valid", []byte{0x1A, 0xE1}, false, 6881}, // 0x1AE1 = 6881
		{"wrong length", []byte{0x1A}, true, 0},    // only 1 byte, port needs exactly 2
	}

	for _, tt := range tests {
		got, err := ParsePortPayload(tt.payload)
		if (err != nil) != tt.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", tt.name, err, tt.wantErr)
			continue
		}
		if !tt.wantErr && got.Port != tt.wantPort {
			t.Errorf("%s: port = %d, want %d", tt.name, got.Port, tt.wantPort)
		}
	}
}
