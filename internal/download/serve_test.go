package download

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/asmanya/p2p-file-distribution/internal/peer"
	"github.com/asmanya/p2p-file-distribution/internal/piece"
	"github.com/asmanya/p2p-file-distribution/internal/storage"
)

// newServeFixture writes deterministic test data to a real file on disk and returns the geometry needed to read it
// back. pieceLength (16) and totalLength (40) give two full pieces plus one short last piece (8 bytes) - the same
// edge case every other layer's geometry tests cover, needed here specifically to test the last-piece bounds fix.
func newServeFixture(t *testing.T) (data []byte, pieceCount int, pieceLength, totalLength int64, sf *storage.File) {
	t.Helper()
	pieceLength = 16
	totalLength = 40
	data = make([]byte, totalLength)
	for i := range data {
		data[i] = byte(i)
	}
	pieceCount = int((totalLength + pieceLength - 1) / pieceLength)

	f, err := storage.Create(filepath.Join(t.TempDir(), "serve-fixture.bin"), totalLength)
	if err != nil {
		t.Fatalf("storage.Create: %v", err)
	}
	t.Cleanup(func() { f.Close() })

	for i := 0; i < pieceCount; i++ {
		start, end, err := piece.Range(i, pieceCount, pieceLength, totalLength)
		if err != nil {
			t.Fatalf("piece.Range: %v", err)
		}
		if err := f.WritePiece(i, pieceCount, pieceLength, totalLength, data[start:end]); err != nil {
			t.Fatalf("WritePiece: %v", err)
		}
	}
	return data, pieceCount, pieceLength, totalLength, f
}

// buildRequestPayload encodes a request message's payload by hand, exactly as a real peer would send it -
// serveRequest's own raw-byte parsing (needed for the last-piece bounds fix) is what's under test here, so this
// helper deliberately doesn't go through peer.SendRequest's serialization path.
func buildRequestPayload(index, begin, length int) []byte {
	payload := make([]byte, 12)
	binary.BigEndian.PutUint32(payload[0:4], uint32(index))
	binary.BigEndian.PutUint32(payload[4:8], uint32(begin))
	binary.BigEndian.PutUint32(payload[8:12], uint32(length))
	return payload
}

func TestServeRequestSendsBlock(t *testing.T) {
	data, pieceCount, pieceLength, totalLength, sf := newServeFixture(t)
	have := NewHaveBitfield(pieceCount)
	have.Set(0)

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	conn := peer.NewConn(server, [20]byte{1}, [8]byte{})
	conn.AmChoking = false

	progress := NewProgress(pieceCount)
	payload := buildRequestPayload(0, 2, 5)

	errCh := make(chan error, 1)
	go func() {
		errCh <- serveRequest(conn, payload, pieceCount, pieceLength, totalLength, have, sf, progress)
	}()

	msg, err := peer.ReadMessage(bufio.NewReader(client))
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if msg.ID != peer.MsgPiece {
		t.Fatalf("got message ID %v, want piece", msg.ID)
	}
	got, err := peer.ParsePiecePayload(msg.Payload, pieceCount, int(pieceLength))
	if err != nil {
		t.Fatalf("ParsePiecePayload: %v", err)
	}
	if got.Index != 0 || got.Begin != 2 {
		t.Errorf("got index=%d begin=%d, want 0,2", got.Index, got.Begin)
	}
	if want := data[2:7]; !bytes.Equal(got.Block, want) {
		t.Errorf("got block %v, want %v", got.Block, want)
	}

	if err := <-errCh; err != nil {
		t.Fatalf("serveRequest: %v", err)
	}
	if got := progress.BytesUploaded(); got != 5 {
		t.Errorf("BytesUploaded=%d, want 5", got)
	}
}

func TestServeRequestIgnoresChokedPeer(t *testing.T) {
	_, pieceCount, pieceLength, totalLength, sf := newServeFixture(t)
	have := NewHaveBitfield(pieceCount)
	have.Set(0)

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	conn := peer.NewConn(server, [20]byte{1}, [8]byte{}) // AmChoking defaults to true

	if err := serveRequest(conn, buildRequestPayload(0, 0, 4), pieceCount, pieceLength, totalLength, have, sf, nil); err != nil {
		t.Fatalf("serveRequest: %v", err)
	}

	client.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	if _, err := client.Read(make([]byte, 1)); err == nil {
		t.Fatal("expected no data to be sent to a choked peer")
	}
}

func TestServeRequestIgnoresMissingPiece(t *testing.T) {
	_, pieceCount, pieceLength, totalLength, sf := newServeFixture(t)
	have := NewHaveBitfield(pieceCount) // nothing marked complete

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	conn := peer.NewConn(server, [20]byte{1}, [8]byte{})
	conn.AmChoking = false

	if err := serveRequest(conn, buildRequestPayload(0, 0, 4), pieceCount, pieceLength, totalLength, have, sf, nil); err != nil {
		t.Fatalf("serveRequest: %v", err)
	}

	client.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	if _, err := client.Read(make([]byte, 1)); err == nil {
		t.Fatal("expected no data to be sent for a piece we don't have")
	}
}

func TestServeRequestRejectsOutOfRangeIndex(t *testing.T) {
	_, pieceCount, pieceLength, totalLength, sf := newServeFixture(t)
	have := NewHaveBitfield(pieceCount)

	_, server := net.Pipe()
	defer server.Close()
	conn := peer.NewConn(server, [20]byte{1}, [8]byte{})
	conn.AmChoking = false

	err := serveRequest(conn, buildRequestPayload(pieceCount, 0, 4), pieceCount, pieceLength, totalLength, have, sf, nil)
	if err == nil {
		t.Fatal("expected an error for an out-of-range piece index")
	}
}

func TestServeRequestRejectsLastPieceOverrun(t *testing.T) {
	// Piece 2 is only 8 bytes long (see newServeFixture). A request against it using the standard 16-byte
	// pieceLength for bounds checking would wrongly look valid - this is exactly the bug the two-step parse in
	// serveRequest exists to prevent.
	_, pieceCount, pieceLength, totalLength, sf := newServeFixture(t)
	have := NewHaveBitfield(pieceCount)
	have.Set(2)

	_, server := net.Pipe()
	defer server.Close()
	conn := peer.NewConn(server, [20]byte{1}, [8]byte{})
	conn.AmChoking = false

	err := serveRequest(conn, buildRequestPayload(2, 5, 10), pieceCount, pieceLength, totalLength, have, sf, nil)
	if err == nil {
		t.Fatal("expected an error for a request overrunning the short last piece")
	}
}

func TestServeRequestRejectsOversizedLength(t *testing.T) {
	f, err := storage.Create(filepath.Join(t.TempDir(), "big-fixture.bin"), 20000)
	if err != nil {
		t.Fatalf("storage.Create: %v", err)
	}
	defer f.Close()

	have := NewHaveBitfield(1)
	have.Set(0)

	_, server := net.Pipe()
	defer server.Close()
	conn := peer.NewConn(server, [20]byte{1}, [8]byte{})
	conn.AmChoking = false

	// A single 20000-byte piece: begin=0, length=peer.MaxIncomingRequestSize+1 fits within the piece itself, so
	// only the absolute size cap - not the geometry check - is what should reject this.
	err = serveRequest(conn, buildRequestPayload(0, 0, peer.MaxIncomingRequestSize+1), 1, 20000, 20000, have, f, nil)
	if err == nil {
		t.Fatal("expected an error for a request exceeding MaxIncomingRequestSize")
	}
}
