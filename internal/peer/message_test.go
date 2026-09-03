package peer

import (
	"bytes"
	"testing"
)

func TestMessageSerializeHave(t *testing.T) {
	m := Message{ID: MsgHave, Payload: []byte{0x00, 0x00, 0x00, 0x05}}

	got := m.Serialize()
	want := []byte{0x00, 0x00, 0x00, 0x05, 0x04, 0x00, 0x00, 0x00, 0x05}

	if !bytes.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestMessageSerializeKeepAlive(t *testing.T) {
	m := Message{ID: MsgKeepAlive}

	got := m.Serialize()
	want := []byte{0x00, 0x00, 0x00, 0x00}

	if !bytes.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestMessageSerializeEmptyPayload(t *testing.T) {
	for _, id := range []MessageID{MsgChoke, MsgUnchoke, MsgInterested, MsgNotInterested} {
		m := Message{ID: id}
		got := m.Serialize()
		want := []byte{0x00, 0x00, 0x00, 0x01, byte(id)}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: got %v, want %v", id, got, want)
		}
	}
}

func TestMessageRoundTrip(t *testing.T) {
	tests := []Message{
		{ID: MsgKeepAlive},
		{ID: MsgChoke},
		{ID: MsgUnchoke},
		{ID: MsgHave, Payload: []byte{0x00, 0x00, 0x00, 0x2A}},
		{ID: MsgRequest, Payload: []byte{0, 0, 0, 1, 0, 0, 0, 2, 0, 0, 0, 3}},
		{ID: MsgPiece, Payload: []byte{0, 0, 0, 1, 0, 0, 0, 0, 'd', 'a', 't', 'a'}},
	}

	for _, m := range tests {
		got, err := ReadMessage(bytes.NewReader(m.Serialize()))
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", m.ID, err)
		}
		if got.ID != m.ID || !bytes.Equal(got.Payload, m.Payload) {
			t.Errorf("%s: got %+v, want %+v", m.ID, got, m)
		}
	}
}
