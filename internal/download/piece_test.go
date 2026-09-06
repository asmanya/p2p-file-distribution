package download

import (
	"bytes"
	"encoding/binary"
	"net"
	"os"
	"testing"
	"time"

	"github.com/asmanya/p2p-file-distribution/internal/metainfo"
	"github.com/asmanya/p2p-file-distribution/internal/peer"
	"github.com/asmanya/p2p-file-distribution/internal/piece"
)

// --- Shared fixtures and helpers ---------------------------------------
//
// Every test below plays the role of a peer talking to our own Piece()
// function over net.Pipe(). Because we control both ends, we know exactly
// what should happen - which is what makes these tests deterministic in a
// way a real network connection never is.

// loadFixture parses a small test .torrent and reads the data file it
// describes, so every test knows the exact expected content and hash of
// every piece, without trusting this project's own code to have produced
// that content in the first place.
func loadFixture(t *testing.T) (*metainfo.Torrent, []byte) {
	t.Helper()
	tor, err := metainfo.ParseFile("../../testdata/small.torrent")
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	data, err := os.ReadFile("../../testdata/sample.dat")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	return tor, data
}

// pieceWork builds the immutable Work descriptor for piece index, using
// the same geometry function (piece.Length) that Piece() itself relies on
// internally - so these tests exercise the real piece/block math, not a
// hand-typed duplicate of it that could quietly drift out of sync.
func pieceWork(t *testing.T, tor *metainfo.Torrent, index int) piece.Work {
	t.Helper()
	length, err := piece.Length(index, tor.PieceCount(), tor.PieceLength, tor.TotalLength)
	if err != nil {
		t.Fatalf("piece.Length: %v", err)
	}
	return piece.Work{
		Index:        index,
		ExpectedHash: tor.PiecesHashes[index][:],
		Length:       length,
	}
}

// pieceData slices out the exact bytes piece index should contain, straight
// out of the ground-truth data file - this is what every fake seeder below
// serves, and what every test compares the downloaded result against.
func pieceData(t *testing.T, tor *metainfo.Torrent, data []byte, index int) []byte {
	t.Helper()
	start, end, err := piece.Range(index, tor.PieceCount(), tor.PieceLength, tor.TotalLength)
	if err != nil {
		t.Fatalf("piece.Range: %v", err)
	}
	return data[start:end]
}

// buildPiecePayload packs a piece message's payload by hand: 4-byte piece
// index, 4-byte begin offset, then the raw block bytes - the exact wire
// layout peer.ParsePiecePayload expects to parse on the way back in.
func buildPiecePayload(index, begin int, block []byte) []byte {
	payload := make([]byte, 8+len(block))
	binary.BigEndian.PutUint32(payload[0:4], uint32(index))
	binary.BigEndian.PutUint32(payload[4:8], uint32(begin))
	copy(payload[8:], block)
	return payload
}

// send writes msg to the wire. It's called from the fake-seeder goroutines
// below, which can't call t.Fatal (that's only safe from the test's own
// goroutine) - so a failed write is just logged, and the test itself will
// notice the resulting error or hang on Piece()'s side instead.
func send(t *testing.T, conn net.Conn, msg peer.Message) {
	t.Helper()
	if _, err := conn.Write(msg.Serialize()); err != nil {
		t.Logf("seeder: write: %v", err)
	}
}

// newTestConn wires up a net.Pipe() and wraps the client end in a *peer.Conn,
// the same way a real Dial would after a completed handshake. The returned
// server end is handed to a fake-seeder goroutine that plays the role of
// the remote peer for the rest of the test.
func newTestConn(t *testing.T) (conn *peer.Conn, server net.Conn) {
	t.Helper()
	client, server := net.Pipe()
	conn = peer.NewConn(client, [20]byte{}, [8]byte{})
	t.Cleanup(func() { conn.Close() })
	return conn, server
}

// runFullSeeder plays a well-behaved peer for an entire piece download:
// unchoke immediately, then answer every request with the correct block -
// or, if corruptFirstBlock is set, flip one byte in the very first block it
// sends, to simulate corruption.
//
// It reads incoming messages and writes outgoing responses on two separate
// goroutines, joined only by the outbox channel, rather than answering each
// request inline as it's read. That split matters: net.Pipe() has no
// internal buffering, so a Write blocks until something on the other end
// Reads it. The real download loop pipelines several requests before it
// reads any response back - so if this seeder answered inline (read a
// request, write its response, read the next request), the client's next
// pipelined request-Write and this function's in-progress response-Write
// would both end up waiting on a Read neither side is about to do. That's a
// deadlock, and without this split it would only show up as an unexplained
// multi-second timeout instead of a clear failure. Decoupling the reader
// from the writer means the reader keeps draining incoming requests no
// matter how far behind the writer's responses are.
func runFullSeeder(t *testing.T, server net.Conn, tor *metainfo.Torrent, work piece.Work, expected []byte, corruptFirstBlock bool) {
	t.Helper()

	outbox := make(chan peer.Message, 8)

	// Writer goroutine: only ever sends what's placed in outbox, one
	// message at a time, however long each Write takes to be read.
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for msg := range outbox {
			send(t, server, msg)
		}
	}()
	defer func() {
		close(outbox)
		<-writerDone
		server.Close()
	}()

	// Reader loop: never blocked by the writer above, so it can keep
	// accepting new requests even while several responses are still
	// queued up waiting to be sent.
	corrupted := false
	for {
		msg, err := peer.ReadMessage(server)
		if err != nil {
			return // client closed the connection - nothing more to serve
		}
		switch msg.ID {
		case peer.MsgInterested:
			outbox <- peer.Message{ID: peer.MsgUnchoke}
		case peer.MsgRequest:
			req, err := peer.ParseRequestPayload(msg.Payload, tor.PieceCount(), int(work.Length))
			if err != nil {
				return
			}
			block := make([]byte, req.Length)
			copy(block, expected[req.Begin:req.Begin+req.Length])
			if corruptFirstBlock && !corrupted {
				block[0] ^= 0xFF // flip one byte - still arrives as valid-looking data, just wrong
				corrupted = true
			}
			outbox <- peer.Message{ID: peer.MsgPiece, Payload: buildPiecePayload(work.Index, req.Begin, block)}
		}
	}
}

// --- Case 1: happy path -------------------------------------------------
//
// Peer unchokes immediately and answers every request correctly. This is
// the baseline every other test below is a variation on.
func TestPieceHappyPath(t *testing.T) {
	tor, data := loadFixture(t)
	work := pieceWork(t, tor, 0)
	expected := pieceData(t, tor, data, 0)

	conn, server := newTestConn(t)
	go runFullSeeder(t, server, tor, work, expected, false)

	if err := EnsureUnchoked(conn); err != nil {
		t.Fatalf("EnsureUnchoked: %v", err)
	}
	got, err := Piece(conn, work, tor.PieceCount(), nil)
	if err != nil {
		t.Fatalf("Piece: %v", err)
	}
	if !bytes.Equal(got, expected) {
		t.Error("downloaded bytes don't match expected piece content")
	}
}

// --- Case 2: corrupted block ---------------------------------------------
//
// The seeder flips one byte in the very first block it serves. Every byte
// still arrives over the wire looking like a normal, complete message - the
// framing and parsing layers have no way to know anything is wrong - so
// this can only be caught by the final SHA-1 check. If this test ever
// passes without that check actually running, something upstream broke
// silently.
func TestPieceCorruptedBlockFailsHash(t *testing.T) {
	tor, data := loadFixture(t)
	work := pieceWork(t, tor, 0)
	expected := pieceData(t, tor, data, 0)

	conn, server := newTestConn(t)
	go runFullSeeder(t, server, tor, work, expected, true)

	if err := EnsureUnchoked(conn); err != nil {
		t.Fatalf("EnsureUnchoked: %v", err)
	}
	if _, err := Piece(conn, work, tor.PieceCount(), nil); err == nil {
		t.Fatal("expected hash mismatch error, got nil")
	}
}

// --- Case 3: out-of-order blocks -----------------------------------------
//
// The test piece here is exactly two blocks, which fits comfortably under
// the pipelining backlog limit - so the client fires off both requests
// before it ever reads a response. That lets this fake seeder collect both
// requests first, then deliberately answer them in reverse. Correct
// assembly depends on each block being copied to its own begin offset in
// the output buffer, not on blocks arriving in the same order they were
// requested.
func TestPieceOutOfOrderBlocksAssembleCorrectly(t *testing.T) {
	tor, data := loadFixture(t)
	work := pieceWork(t, tor, 0)
	expected := pieceData(t, tor, data, 0)

	conn, server := newTestConn(t)

	// This seeder is scripted rather than a generic read/respond loop,
	// because the whole point of the test is controlling the exact order
	// responses go out in.
	go func() {
		defer server.Close()

		if _, err := peer.ReadMessage(server); err != nil { // "interested"
			return
		}
		send(t, server, peer.Message{ID: peer.MsgUnchoke})

		// Collect both in-flight requests before answering either one.
		var reqs []peer.RequestPayload
		for len(reqs) < 2 {
			msg, err := peer.ReadMessage(server)
			if err != nil {
				return
			}
			req, err := peer.ParseRequestPayload(msg.Payload, tor.PieceCount(), int(work.Length))
			if err != nil {
				return
			}
			reqs = append(reqs, req)
		}

		// Now answer the most-recently-requested block first.
		for i := len(reqs) - 1; i >= 0; i-- {
			req := reqs[i]
			block := expected[req.Begin : req.Begin+req.Length]
			send(t, server, peer.Message{ID: peer.MsgPiece, Payload: buildPiecePayload(work.Index, req.Begin, block)})
		}
	}()

	if err := EnsureUnchoked(conn); err != nil {
		t.Fatalf("EnsureUnchoked: %v", err)
	}
	got, err := Piece(conn, work, tor.PieceCount(), nil)
	if err != nil {
		t.Fatalf("Piece: %v", err)
	}
	if !bytes.Equal(got, expected) {
		t.Error("downloaded bytes don't match expected piece content")
	}
}

// --- Case 4: choke mid-download, then resume ------------------------------
//
// This seeder is fully scripted, step by step, so the choke lands at an
// exact, known point in the exchange:
//
//  1. client sends "interested"                        -> seeder sends unchoke
//  2. client sends request(block 0), request(block 1)  (both fit under the backlog limit)
//  3. seeder answers block 0, then sends choke instead of answering block 1
//  4. the client's choke handling must reset its backlog and rewind its
//     next-block pointer back to block 1 - a peer that chokes mid-download
//     silently drops every request already in flight, and if that isn't
//     accounted for, the client ends up waiting forever for a response
//     that is never coming
//  5. seeder sends unchoke
//  6. client re-requests block 1; seeder answers it for real this time
//
// If the choke-handling logic is missing or wrong, this test hangs until
// the piece-level timeout instead of completing.
func TestPieceChokeThenUnchokeResumes(t *testing.T) {
	tor, data := loadFixture(t)
	work := pieceWork(t, tor, 0)
	expected := pieceData(t, tor, data, 0)

	conn, server := newTestConn(t)

	go func() {
		defer server.Close()

		if _, err := peer.ReadMessage(server); err != nil { // "interested"
			return
		}
		send(t, server, peer.Message{ID: peer.MsgUnchoke})

		msg1, err := peer.ReadMessage(server) // request for block 0
		if err != nil {
			return
		}
		req1, err := peer.ParseRequestPayload(msg1.Payload, tor.PieceCount(), int(work.Length))
		if err != nil {
			return
		}
		if _, err := peer.ReadMessage(server); err != nil { // request for block 1 - about to be dropped
			return
		}

		// Answer block 0 for real, then choke before ever answering block 1.
		block1 := expected[req1.Begin : req1.Begin+req1.Length]
		send(t, server, peer.Message{ID: peer.MsgPiece, Payload: buildPiecePayload(work.Index, req1.Begin, block1)})
		send(t, server, peer.Message{ID: peer.MsgChoke})
		send(t, server, peer.Message{ID: peer.MsgUnchoke}) // the client should re-request block 1 after this

		msg2, err := peer.ReadMessage(server) // the re-request
		if err != nil {
			return
		}
		req2, err := peer.ParseRequestPayload(msg2.Payload, tor.PieceCount(), int(work.Length))
		if err != nil {
			return
		}
		block2 := expected[req2.Begin : req2.Begin+req2.Length]
		send(t, server, peer.Message{ID: peer.MsgPiece, Payload: buildPiecePayload(work.Index, req2.Begin, block2)})
	}()

	if err := EnsureUnchoked(conn); err != nil {
		t.Fatalf("EnsureUnchoked: %v", err)
	}
	got, err := Piece(conn, work, tor.PieceCount(), nil)
	if err != nil {
		t.Fatalf("Piece: %v", err)
	}
	if !bytes.Equal(got, expected) {
		t.Error("downloaded bytes don't match expected piece content")
	}
}

// --- Case 5: peer goes silent mid-piece ------------------------------------
//
// readTimeout is shrunk for this test only (it's a package-level var
// instead of a const for exactly this reason - see piece.go), so the test
// doesn't have to wait out the real default. The seeder answers one block,
// then simply stops responding - Piece() must return a clean error once the
// idle read deadline trips, rather than hang forever waiting for a peer
// that has gone silent.
func TestPieceGoesSilentTriggersReadTimeout(t *testing.T) {
	oldRead := readTimeout
	readTimeout = 200 * time.Millisecond
	defer func() { readTimeout = oldRead }()

	tor, data := loadFixture(t)
	work := pieceWork(t, tor, 0)
	expected := pieceData(t, tor, data, 0)

	conn, server := newTestConn(t)

	go func() {
		defer server.Close()

		if _, err := peer.ReadMessage(server); err != nil {
			return
		}
		send(t, server, peer.Message{ID: peer.MsgUnchoke})

		// The client pipelines both of this piece's block requests before
		// it ever reads a response - both writes need a matching Read here
		// before either can complete, or the client would block on its own
		// second Write instead of ever reaching the read-timeout this test
		// means to exercise. So drain both requests up front.
		msg1, err := peer.ReadMessage(server)
		if err != nil {
			return
		}
		if _, err := peer.ReadMessage(server); err != nil {
			return
		}

		// Answer only the first block, for real.
		req, err := peer.ParseRequestPayload(msg1.Payload, tor.PieceCount(), int(work.Length))
		if err != nil {
			return
		}
		block := expected[req.Begin : req.Begin+req.Length]
		send(t, server, peer.Message{ID: peer.MsgPiece, Payload: buildPiecePayload(work.Index, req.Begin, block)})

		// Then go silent on purpose and never answer the second request.
		time.Sleep(2 * time.Second)
	}()

	if err := EnsureUnchoked(conn); err != nil {
		t.Fatalf("EnsureUnchoked: %v", err)
	}
	if _, err := Piece(conn, work, tor.PieceCount(), nil); err == nil {
		t.Fatal("expected a read-timeout error, got nil")
	}
}

// --- Case 6: peer never unchokes -------------------------------------------
//
// unchokeTimeout is shrunk the same way as readTimeout above. The seeder
// reads "interested" and then does nothing at all - EnsureUnchoked (the
// one-time setup step that owns this wait, not Piece() itself) must give up
// with a clean timeout error rather than wait forever for a peer that never
// intends to serve us.
func TestPieceNeverUnchokesTimesOut(t *testing.T) {
	oldUnchoke := unchokeTimeout
	unchokeTimeout = 200 * time.Millisecond
	defer func() { unchokeTimeout = oldUnchoke }()

	conn, server := newTestConn(t)

	go func() {
		defer server.Close()
		peer.ReadMessage(server) // reads "interested", deliberately never responds
		time.Sleep(2 * time.Second)
	}()

	if err := EnsureUnchoked(conn); err == nil {
		t.Fatal("expected an unchoke-timeout error, got nil")
	}
}

// --- Case 7: malicious out-of-range piece index ----------------------------
//
// The seeder unchokes normally, then answers the first request with a piece
// message claiming a piece index that doesn't exist in this torrent at all.
// Every incoming piece message's index is validated against the torrent's
// actual piece count before it's trusted; this test is the proof that a
// malformed message from a hostile or buggy peer produces a clean error
// here, not a crash or an out-of-bounds write into the output buffer.
func TestPieceOutOfRangeIndexRejected(t *testing.T) {
	tor, _ := loadFixture(t)
	work := pieceWork(t, tor, 0)

	conn, server := newTestConn(t)

	go func() {
		defer server.Close()

		if _, err := peer.ReadMessage(server); err != nil {
			return
		}
		send(t, server, peer.Message{ID: peer.MsgUnchoke})

		// The client pipelines both of this piece's block requests before
		// it ever reads a response - drain both first, so neither write
		// blocks waiting for a read that would otherwise never come before
		// the bad response below is even sent.
		if _, err := peer.ReadMessage(server); err != nil {
			return
		}
		if _, err := peer.ReadMessage(server); err != nil {
			return
		}
		badPayload := buildPiecePayload(999999, 0, make([]byte, piece.BlockSize))
		send(t, server, peer.Message{ID: peer.MsgPiece, Payload: badPayload})
	}()

	if err := EnsureUnchoked(conn); err != nil {
		t.Fatalf("EnsureUnchoked: %v", err)
	}
	if _, err := Piece(conn, work, tor.PieceCount(), nil); err == nil {
		t.Fatal("expected error for out-of-range piece index, got nil")
	}
}

// --- Case 8: last, shorter piece --------------------------------------------
//
// The test fixture's total length isn't an exact multiple of its piece
// length, so the last piece is shorter than every other piece in the
// torrent. This exercises the piece/block geometry math end to end, inside
// the actual download loop - a bug there could pass its own isolated unit
// tests yet still be wired in wrong here (for example, requesting one block
// too many, or verifying the hash against the wrong length).
func TestPieceLastShortPiece(t *testing.T) {
	tor, data := loadFixture(t)
	lastIndex := tor.PieceCount() - 1
	work := pieceWork(t, tor, lastIndex)
	expected := pieceData(t, tor, data, lastIndex)

	conn, server := newTestConn(t)
	go runFullSeeder(t, server, tor, work, expected, false)

	if err := EnsureUnchoked(conn); err != nil {
		t.Fatalf("EnsureUnchoked: %v", err)
	}
	got, err := Piece(conn, work, tor.PieceCount(), nil)
	if err != nil {
		t.Fatalf("Piece: %v", err)
	}
	if len(got) != int(work.Length) {
		t.Errorf("got length %d, want %d", len(got), work.Length)
	}
	if !bytes.Equal(got, expected) {
		t.Error("downloaded bytes don't match expected piece content")
	}
}

// --- Case 9: one connection, two pieces, one unchoke -------------------------
//
// EnsureUnchoked and Piece are deliberately separate calls so a connection
// can be reused across many pieces without repeating the interested/unchoke
// handshake for each one. This test is the direct proof of that: it calls
// EnsureUnchoked exactly once, then Piece twice for two different pieces on
// the same connection. Before that split existed, Piece did its own
// interested-send-and-wait-for-unchoke on every call - which would have
// hung here, since a peer that already unchoked us has no reason to send a
// second, redundant unchoke message just because we asked for another piece.
func TestPieceMultipleDownloadsOnSameConnection(t *testing.T) {
	tor, data := loadFixture(t)
	work0 := pieceWork(t, tor, 0)
	work1 := pieceWork(t, tor, 1)
	expected0 := pieceData(t, tor, data, 0)
	expected1 := pieceData(t, tor, data, 1)
	byIndex := map[int][]byte{0: expected0, 1: expected1}

	conn, server := newTestConn(t)

	// Same reader/writer split as runFullSeeder, for the same reason: two
	// pieces means up to four pipelined block requests can arrive before
	// this seeder's first response is read, so reading and writing must
	// not block each other.
	go func() {
		outbox := make(chan peer.Message, 8)
		writerDone := make(chan struct{})
		go func() {
			defer close(writerDone)
			for msg := range outbox {
				send(t, server, msg)
			}
		}()
		defer func() {
			close(outbox)
			<-writerDone
			server.Close()
		}()

		for {
			msg, err := peer.ReadMessage(server)
			if err != nil {
				return
			}
			switch msg.ID {
			case peer.MsgInterested:
				outbox <- peer.Message{ID: peer.MsgUnchoke}
			case peer.MsgRequest:
				req, err := peer.ParseRequestPayload(msg.Payload, tor.PieceCount(), int(tor.PieceLength))
				if err != nil {
					return
				}
				expected, ok := byIndex[req.Index]
				if !ok {
					return
				}
				block := expected[req.Begin : req.Begin+req.Length]
				outbox <- peer.Message{ID: peer.MsgPiece, Payload: buildPiecePayload(req.Index, req.Begin, block)}
			}
		}
	}()

	if err := EnsureUnchoked(conn); err != nil {
		t.Fatalf("EnsureUnchoked: %v", err)
	}

	got0, err := Piece(conn, work0, tor.PieceCount(), nil)
	if err != nil {
		t.Fatalf("Piece(0): %v", err)
	}
	if !bytes.Equal(got0, expected0) {
		t.Error("piece 0: downloaded bytes don't match expected content")
	}

	// No second EnsureUnchoked call - the whole point of this test.
	got1, err := Piece(conn, work1, tor.PieceCount(), nil)
	if err != nil {
		t.Fatalf("Piece(1): %v", err)
	}
	if !bytes.Equal(got1, expected1) {
		t.Error("piece 1: downloaded bytes don't match expected content")
	}
}
