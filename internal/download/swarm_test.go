package download

import (
	"bytes"
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/asmanya/p2p-file-distribution/internal/metainfo"
	"github.com/asmanya/p2p-file-distribution/internal/peer"
	"github.com/asmanya/p2p-file-distribution/internal/piece"
	"github.com/asmanya/p2p-file-distribution/internal/tracker"
)

// swarmBehavior controls how one fake seeder in TestDownloadLocalSwarm
// responds, so the test can exercise the requeue and hash-rejection paths a
// swarm of only well-behaved peers would never trigger.
type swarmBehavior struct {
	dieAfter int           // 0 means never - otherwise close the connection after serving this many whole pieces
	corrupt  bool          // flip one byte in every block served
	delay    time.Duration // sleep this long before every block response
}

// startSwarmSeeder listens on a free local port, accepts exactly one
// connection, and performs the server side of the handshake by hand - this
// project's peer package is dial-only, it never accepts connections, so the
// test has to play the remote peer itself. Once the handshake is done it
// hands off to runSwarmSeeder for the rest of the exchange.
func startSwarmSeeder(t *testing.T, tor *metainfo.Torrent, data []byte, behavior swarmBehavior) netip.AddrPort {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return // listener closed at test teardown - not a real failure
		}
		defer conn.Close()

		theirs, err := peer.ParseHandshake(conn)
		if err != nil {
			return
		}
		ours := peer.Handshake{InfoHash: theirs.InfoHash, PeerID: [20]byte{}}
		if _, err := conn.Write(ours.Serialize()); err != nil {
			return
		}

		// receiveBitfield (called by worker() right after Dial) blocks
		// waiting to read something for up to bitfieldWaitTimeout before it
		// gives up and assumes "no pieces yet" - a real seeder volunteers its
		// bitfield unprompted immediately after the handshake, and without
		// this every worker in the test would burn the full timeout waiting
		// for a message neither side sends first.
		full := make(peer.Bitfield, (tor.PieceCount()+7)/8)
		for i := 0; i < tor.PieceCount(); i++ {
			full.SetPiece(i)
		}
		bitfieldMsg := peer.Message{ID: peer.MsgBitfield, Payload: full}
		if _, err := conn.Write(bitfieldMsg.Serialize()); err != nil {
			return
		}

		runSwarmSeeder(t, conn, tor, data, behavior)
	}()

	return netip.MustParseAddrPort(ln.Addr().String())
}

// runSwarmSeeder answers interested/request messages the same way
// runFullSeeder in piece_test.go does, generalized to serve any piece index
// (not just one fixed Work) and to apply the requested failure behavior.
func runSwarmSeeder(t *testing.T, conn net.Conn, tor *metainfo.Torrent, data []byte, behavior swarmBehavior) {
	t.Helper()

	outbox := make(chan peer.Message, 8)
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for msg := range outbox {
			if behavior.delay > 0 && msg.ID == peer.MsgPiece {
				time.Sleep(behavior.delay)
			}
			if _, err := conn.Write(msg.Serialize()); err != nil {
				return
			}
		}
	}()
	defer func() {
		close(outbox)
		<-writerDone
	}()

	piecesServed := 0
	for {
		msg, err := peer.ReadMessage(conn)
		if err != nil {
			return // downloader closed the connection
		}

		switch msg.ID {
		case peer.MsgInterested:
			outbox <- peer.Message{ID: peer.MsgUnchoke}

		case peer.MsgRequest:
			// tor.PieceLength is a safe upper bound for every piece, including
			// the shorter last one - it's only used here to validate what a
			// well-behaved client sent us, never to size anything.
			req, err := peer.ParseRequestPayload(msg.Payload, tor.PieceCount(), int(tor.PieceLength))
			if err != nil {
				return
			}
			start, end, err := piece.Range(req.Index, tor.PieceCount(), tor.PieceLength, tor.TotalLength)
			if err != nil {
				return
			}
			pieceBytes := data[start:end]
			if req.Begin+req.Length > len(pieceBytes) {
				return
			}

			block := make([]byte, req.Length)
			copy(block, pieceBytes[req.Begin:req.Begin+req.Length])
			if behavior.corrupt {
				block[0] ^= 0xFF
			}
			outbox <- peer.Message{ID: peer.MsgPiece, Payload: buildPiecePayload(req.Index, req.Begin, block)}

			if req.Begin+req.Length >= len(pieceBytes) {
				piecesServed++
				if behavior.dieAfter > 0 && piecesServed >= behavior.dieAfter {
					return
				}
			}
		}
	}
}

// TestDownloadLocalSwarm runs a full Download() against an in-process fake
// tracker and five fake seeders with different behaviors - two well-behaved,
// one that dies partway through, one that always sends corrupt data, and one
// that's just slow. The download must still complete and match the fixture
// file exactly: the two healthy seeders are enough to cover every piece the
// unreliable ones fail to deliver.
func TestDownloadLocalSwarm(t *testing.T) {
	tor, data := loadFixture(t)

	addrs := []netip.AddrPort{
		startSwarmSeeder(t, tor, data, swarmBehavior{}),                             // normal
		startSwarmSeeder(t, tor, data, swarmBehavior{}),                             // normal
		startSwarmSeeder(t, tor, data, swarmBehavior{dieAfter: 5}),                  // dies after 5 pieces
		startSwarmSeeder(t, tor, data, swarmBehavior{corrupt: true}),                // always corrupts
		startSwarmSeeder(t, tor, data, swarmBehavior{delay: 20 * time.Millisecond}), // slow
	}
	tor.Announce = fakeTrackerServer(t, addrs)
	tc := tracker.NewClient()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	got, err := Download(ctx, tor, tc, [20]byte{})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Error("downloaded bytes don't match the fixture file")
	}
}
