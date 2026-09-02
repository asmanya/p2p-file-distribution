package peer

import (
	"bytes"
	"strings"
	"testing"
)

func TestHandShakeSerialize(t *testing.T) {
	h := Handshake{
		Reserved: [8]byte{},
		InfoHash: [20]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20},
		PeerID:   [20]byte{21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39, 40},
	}

	got := h.Serialize()

	want := []byte{19}
	want = append(want, "BitTorrent protocol"...)
	want = append(want, h.Reserved[:]...)
	want = append(want, h.InfoHash[:]...)
	want = append(want, h.PeerID[:]...)

	if !bytes.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if len(got) != 68 {
		t.Fatalf("expected 68 bytes, got %d", len(got))
	}
}

func TestParseHandshakeRoundTrip(t *testing.T) {
	h := Handshake{
		InfoHash: [20]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20},
		PeerID:   [20]byte{21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39, 40},
	}

	// Parsing is opposite to serialize, so we should get h
	got, err := ParseHandshake(bytes.NewReader(h.Serialize()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != h {
		t.Fatalf("got %+v, want %+v", got, h)
	}
}

func TestParseHandshakeInvalidLengthPrefix(t *testing.T) {
	buf := make([]byte, handshakeLen)
	buf[0] = 20 // wrong - must be 19

	if _, err := ParseHandshake(bytes.NewReader(buf)); err == nil {
		t.Fatal("expected error for invalid protocol string length")
	}
}

func TestParseHandshakeWrongProtocolString(t *testing.T) {
	buf := make([]byte, handshakeLen)
	buf[0] = 19
	copy(buf[1:20], strings.Repeat("X", 19))

	if _, err := ParseHandshake(bytes.NewReader(buf)); err == nil {
		t.Fatal("expected error for wrong protocol string")
	}
}

func TestParseHandshakeTruncated(t *testing.T) {
	buf := make([]byte, handshakeLen-1) // one byte short
	buf[0] = 19
	copy(buf[1:20], "BitTorrent protocol")

	if _, err := ParseHandshake(bytes.NewReader(buf)); err == nil {
		t.Fatal("expected error for truncated input")
	}
}

func TestParseHandshakeEmpty(t *testing.T) {
	if _, err := ParseHandshake(bytes.NewReader(nil)); err == nil {
		t.Fatal("expected error for empty input")
	}
}
