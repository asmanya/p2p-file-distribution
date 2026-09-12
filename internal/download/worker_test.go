package download

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/asmanya/p2p-file-distribution/internal/peer"
)

// --- Connection-level tests ---------------------------------------------
//
// These drive a real runConnection against a net.Pipe, with a live coordinator goroutine on the other side of
// the events channel. They exist because the bugs they cover were invisible to every unit test in this package:
// each individual piece behaved correctly, and seeding still did nothing at all.

// newLiveCoordinator starts a coordinator goroutine, which a connection loop needs: runConnection blocks sending
// events, so without something receiving them nothing past the first event ever happens.
func newLiveCoordinator(t *testing.T, pieceCount int, complete bool) (*Coordinator, context.Context) {
	t.Helper()
	c, _ := newTestCoordinator(pieceCount)
	if complete {
		for i := 0; i < pieceCount; i++ {
			c.MarkComplete(i)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go c.Run(ctx)
	return c, ctx
}

func TestConnectionSendsOurBitfieldFirst(t *testing.T) {
	const pieceCount = 8
	c, ctx := newLiveCoordinator(t, pieceCount, false)

	have := NewHaveBitfield(pieceCount)
	have.Set(0)
	have.Set(3)

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	go serveIncoming(ctx, peer.NewConn(server, [20]byte{}, [8]byte{}), netip.MustParseAddrPort("127.0.0.1:1"),
		pieceCount, c, nil, nil, &SeedConfig{PieceLength: 16, TotalLength: 16 * pieceCount, Have: have})

	if err := client.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	msg, err := peer.ReadMessage(client)
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if msg.ID != peer.MsgBitfield {
		t.Fatalf("first message was %v, want bitfield - a peer that never learns what we have never asks us for anything", msg.ID)
	}
	bf := peer.Bitfield(msg.Payload)
	if !bf.HasPiece(0) || !bf.HasPiece(3) || bf.HasPiece(1) {
		t.Errorf("bitfield %08b doesn't match the pieces we actually have (0 and 3)", msg.Payload)
	}
}

func TestCompleteClientDoesNotDeclareInterest(t *testing.T) {
	const pieceCount = 4
	c, ctx := newLiveCoordinator(t, pieceCount, true)

	have := NewHaveBitfield(pieceCount)
	for i := 0; i < pieceCount; i++ {
		have.Set(i)
	}

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	go serveIncoming(ctx, peer.NewConn(server, [20]byte{}, [8]byte{}), netip.MustParseAddrPort("127.0.0.1:1"),
		pieceCount, c, nil, nil, &SeedConfig{PieceLength: 16, TotalLength: 64, Have: have})

	if err := client.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if msg, err := peer.ReadMessage(client); err != nil || msg.ID != peer.MsgBitfield {
		t.Fatalf("first message = %v (err %v), want bitfield", msg.ID, err)
	}

	// A client holding every piece has nothing to want. Anything arriving here is us asking for data we already
	// have.
	if err := client.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if msg, err := peer.ReadMessage(client); err == nil {
		t.Errorf("a client with every piece sent %v, want nothing at all", msg.ID)
	}
}

// TestIncomingLeecherIsServedWithoutEverUnchokingUs is the end-to-end seeding path, and the regression test for
// three separate bugs: we never sent our bitfield, we tore the connection down while waiting to be unchoked by a
// peer that had no reason to unchoke us, and requests were only answered when nothing else was going on.
func TestIncomingLeecherIsServedWithoutEverUnchokingUs(t *testing.T) {
	oldChoke := chokeInterval
	chokeInterval = 50 * time.Millisecond
	defer func() { chokeInterval = oldChoke }()

	data, pieceCount, pieceLength, totalLength, sf := newServeFixture(t)

	have := NewHaveBitfield(pieceCount)
	for i := 0; i < pieceCount; i++ {
		have.Set(i)
	}
	c, ctx := newLiveCoordinator(t, pieceCount, true)

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	go serveIncoming(ctx, peer.NewConn(server, [20]byte{}, [8]byte{}), netip.MustParseAddrPort("127.0.0.1:1"),
		pieceCount, c, nil, nil, &SeedConfig{PieceLength: pieceLength, TotalLength: totalLength, Have: have, File: sf})

	if err := client.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if msg, err := peer.ReadMessage(client); err != nil || msg.ID != peer.MsgBitfield {
		t.Fatalf("first message = %v (err %v), want bitfield", msg.ID, err)
	}

	// Play a leecher: declare interest, and deliberately never unchoke - we have nothing this seeder wants.
	if _, err := client.Write(peer.Message{ID: peer.MsgInterested}.Serialize()); err != nil {
		t.Fatalf("write interested: %v", err)
	}

	msg, err := peer.ReadMessage(client)
	if err != nil {
		t.Fatalf("waiting for unchoke: %v", err)
	}
	if msg.ID != peer.MsgUnchoke {
		t.Fatalf("got %v, want unchoke once the choking algorithm ran", msg.ID)
	}

	if _, err := client.Write(peer.Message{ID: peer.MsgRequest, Payload: buildRequestPayload(1, 4, 6)}.Serialize()); err != nil {
		t.Fatalf("write request: %v", err)
	}

	msg, err = peer.ReadMessage(client)
	if err != nil {
		t.Fatalf("waiting for the requested block: %v", err)
	}
	if msg.ID != peer.MsgPiece {
		t.Fatalf("got %v, want the piece message answering our request", msg.ID)
	}
	got, err := peer.ParsePiecePayload(msg.Payload, pieceCount, int(pieceLength))
	if err != nil {
		t.Fatalf("ParsePiecePayload: %v", err)
	}
	start := int(pieceLength) + 4
	if want := data[start : start+6]; string(got.Block) != string(want) {
		t.Errorf("served block %v, want %v", got.Block, want)
	}
}

// TestReadLoopExitsWithAMessagePending covers the goroutine leak: closing a connection doesn't wake a reader
// already parked on a channel send, so without the done channel every connection that ended holding one
// unread message left a goroutine behind forever.
func TestReadLoopExitsWithAMessagePending(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	conn := peer.NewConn(client, [20]byte{}, [8]byte{})
	messages := make(chan peer.Message) // deliberately never read from
	done := make(chan struct{})

	exited := make(chan struct{})
	go func() {
		defer close(exited)
		readLoop(conn, messages, done)
	}()

	go func() {
		_, _ = server.Write(peer.Message{ID: peer.MsgUnchoke}.Serialize())
	}()
	time.Sleep(100 * time.Millisecond) // let readLoop park on the send

	close(done)
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("readLoop stayed blocked on a message nobody was reading: one leaked goroutine per finished connection")
	}
}
