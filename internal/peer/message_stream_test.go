package peer

import (
	"bytes"
	"net"
	"testing"
)

// A run-of-the-mill sequence of messages back to back must all parse in order.
func TestReadMessageNormalSequence(t *testing.T) {
	msgs := []Message{
		{ID: MsgUnchoke},
		{ID: MsgHave, Payload: []byte{0, 0, 0, 5}},
		{ID: MsgInterested},
	}

	var buf bytes.Buffer
	for _, m := range msgs {
		buf.Write(m.Serialize())
	}

	for _, want := range msgs {
		got, err := ReadMessage(&buf)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.ID != want.ID || !bytes.Equal(got.Payload, want.Payload) {
			t.Errorf("got %+v, want %+v", got, want)
		}
	}
}

// Keep-alives mixed in between real messages must not disrupt the stream -
// each one is read and returned as its own Message, then reading continues.
func TestReadMessageKeepAlivesInterspersed(t *testing.T) {
	var buf bytes.Buffer
	buf.Write((Message{ID: MsgKeepAlive}).Serialize())
	buf.Write((Message{ID: MsgUnchoke}).Serialize())
	buf.Write((Message{ID: MsgKeepAlive}).Serialize())

	wantIDs := []MessageID{MsgKeepAlive, MsgUnchoke, MsgKeepAlive}
	for _, wantID := range wantIDs {
		got, err := ReadMessage(&buf)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.ID != wantID {
			t.Errorf("got %s, want %s", got.ID, wantID)
		}
	}
}

// A hostile length prefix must be rejected before the body is allocated -
// this is the guard-before-allocate check, not an OOM waiting to happen.
func TestReadMessageHugeLengthRejected(t *testing.T) {
	var lenBuf [4]byte
	// A length far bigger than any real message, and bigger than MaxMessageLength.
	lenBuf[0] = 0x7F
	lenBuf[1] = 0xFF
	lenBuf[2] = 0xFF
	lenBuf[3] = 0xFF

	_, err := ReadMessage(bytes.NewReader(lenBuf[:]))
	if err == nil {
		t.Fatal("expected error for oversized length prefix")
	}
}

// A length prefix that promises more bytes than actually arrive (stream
// ends early) must be a clean error, not a panic on a short read.
func TestReadMessageTruncatedPayload(t *testing.T) {
	full := (Message{ID: MsgHave, Payload: []byte{0, 0, 0, 5}}).Serialize()
	truncated := full[:len(full)-2] // length still says 5 bytes follow, but only 3 are actually here

	_, err := ReadMessage(bytes.NewReader(truncated))
	if err == nil {
		t.Fatal("expected error for truncated payload")
	}
}

// An ID this client doesn't recognize must still parse - ReadMessage's job
// is just framing, not deciding what to do with a message. Rejecting
// unknown IDs here would break interop with any peer that speaks an
// extension we don't support yet.
func TestReadMessageUnknownID(t *testing.T) {
	// ID 20 is the real BitTorrent Extension Protocol (LTEP) message ID -
	// this client doesn't implement it, so it's a realistic "unknown" case.
	raw := []byte{0, 0, 0, 1, 20} // length=1, ID=20 (unknown to this client), no payload
	got, err := ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("unexpected error for unknown ID: %v", err)
	}
	if got.ID != MessageID(20) {
		t.Errorf("got ID %v, want 20", got.ID)
	}
}

// TCP has no message boundaries - two whole messages can legitimately
// arrive in a single underlying read. Both ReadMessage calls below must
// still parse correctly even though the fake peer wrote them as one blob.
func TestReadMessageTwoInOneRead(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()

	m1 := Message{ID: MsgChoke}
	m2 := Message{ID: MsgUnchoke}

	go func() {
		buf := append(m1.Serialize(), m2.Serialize()...) // both messages sent as one write
		server.Write(buf)
	}()

	got1, err := ReadMessage(client)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got2, err := ReadMessage(client)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got1.ID != MsgChoke || got2.ID != MsgUnchoke {
		t.Errorf("got %s, %s; want choke, unchoke", got1.ID, got2.ID)
	}
}

// The mirror case of the test above: a single message split across three
// separate writes (simulating a slow peer trickling bytes in). io.ReadFull
// inside ReadMessage must keep reading until the whole message is in hand.
func TestReadMessageSplitAcrossWrites(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()

	full := (Message{ID: MsgHave, Payload: []byte{0, 0, 0, 42}}).Serialize()

	go func() {
		server.Write(full[:2])  // part of the length prefix
		server.Write(full[2:5]) // rest of length + the ID byte
		server.Write(full[5:])  // the payload
	}()

	got, err := ReadMessage(client)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != MsgHave {
		t.Errorf("got ID %s, want have", got.ID)
	}
}
