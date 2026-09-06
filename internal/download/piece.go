package download

import (
	"errors"
	"fmt"
	"time"

	"github.com/asmanya/p2p-file-distribution/internal/peer"
	"github.com/asmanya/p2p-file-distribution/internal/piece"
)

// unchokeTimeout, pieceTimeout, and readTimeout are vars, not consts, so
// tests can shrink them temporarily instead of waiting out real timeouts.
var (
	unchokeTimeout = 15 * time.Second
	pieceTimeout   = 30 * time.Second
	readTimeout    = 15 * time.Second
)

const backlogLimit = 5 // TODO: adaptive backlog based on peer speed

var ErrUnchokeTimeout = errors.New("download: peer did not unchoke in time")

// EnsureUnchoked sends "interested" (if we haven't already declared it) and
// blocks until the peer unchokes us, or gives up on us.
//
// This is a one-time, per-connection handshake step, deliberately kept
// separate from Piece(): a worker downloading many pieces from the same
// peer only needs to do this once, right after connecting - calling it
// again before every piece would resend "interested" needlessly and, worse,
// block waiting for a fresh "unchoke" message a peer that already unchoked
// us has no reason to send twice.
func EnsureUnchoked(conn *peer.Conn) error {
	if !conn.AmInterested {
		if err := conn.SendInterested(); err != nil {
			return fmt.Errorf("download: send interested: %w", err)
		}
		conn.AmInterested = true
	}

	if !conn.PeerChoking {
		return nil // already unchoked - e.g. a prior piece on this same conn
	}

	return waitForUnchoke(conn)
}

// Piece downloads a single piece over conn, blocking until the piece is
// fully assembled and hash-verified, or an error occurs. It assumes the
// connection is already interested and unchoked - call EnsureUnchoked once
// per connection before the first call to Piece. pieceCount is the
// torrent's total piece count, needed to validate incoming have/request/
// piece messages against this torrent's geometry.
//
// A choke arriving mid-download is still handled here (see downloadBlocks) -
// only the *initial* wait for the first unchoke lives in EnsureUnchoked.
func Piece(conn *peer.Conn, work piece.Work, pieceCount int, progress *Progress) ([]byte, error) {
	buf, err := downloadBlocks(conn, work, pieceCount, progress)
	if err != nil {
		return nil, err
	}

	// Trust nothing until the bytes match the hash from the .torrent file.
	// A peer can lie, corrupt data in transit, or send blocks for the wrong
	// piece entirely - this is the only check that catches all three.
	ok, err := piece.Verify(buf, work.ExpectedHash)
	if err != nil {
		return nil, fmt.Errorf("download: piece %d: %w", work.Index, err)
	}
	if !ok {
		progress.HashFailed()
		return nil, fmt.Errorf("download: piece %d: hash mismatch", work.Index)
	}

	return buf, nil
}

// waitForUnchoke reads messages until the peer unchokes us, or times out.
func waitForUnchoke(conn *peer.Conn) error {
	deadline := time.Now().Add(unchokeTimeout)
	for time.Now().Before(deadline) {
		if err := conn.SetIODeadline(time.Until(deadline)); err != nil {
			return fmt.Errorf("download: set deadline: %w", err)
		}
		msg, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("download: %w", ErrUnchokeTimeout)
		}

		switch msg.ID {
		case peer.MsgUnchoke:
			conn.PeerChoking = false
			return nil
		case peer.MsgChoke:
			conn.PeerChoking = true
		case peer.MsgHave, peer.MsgBitfield, peer.MsgKeepAlive:
			// ignored here - bitfield/have tracking is the caller's job once this function returns; during the
			// unchoke wait we only care about the choke state
		}
	}

	return ErrUnchokeTimeout
}

// downloadBlocks requests and assembles every block of work, pipelining up
// to backlogLimit requests at a time so round-trip latency overlaps across
// blocks instead of stacking up one request at a time. pieceTimeout bounds
// the whole call; readTimeout only bounds a single read and is effectively
// reset every time SetIODeadline is called again below, so a peer that's
// merely slow (but still making progress) survives, while one that's gone
// silent gets dropped.
func downloadBlocks(conn *peer.Conn, work piece.Work, pieceCount int, progress *Progress) ([]byte, error) {
	buf := make([]byte, work.Length)
	var downloaded int64 // bytes actually copied into buf so far
	backlog := 0         // requests currently in flight, unanswered
	nextBlock := 0       // index of the next block we haven't requested yet

	// work.Length is already this piece's resolved (possibly short) length, so BlockCount/BlockBounds are called
	// as if this were a "torrent" of exactly one piece - reusing the tested geometry math instead of re-deriving
	// block-size arithmetic here.
	numBlocks, err := piece.BlockCount(0, 1, work.Length, work.Length)
	if err != nil {
		return nil, fmt.Errorf("download: piece %d: %w", work.Index, err)
	}

	// Fixed cap on the whole call, checked once per outer-loop iteration -
	// this is the "give up no matter what" ceiling, separate from the
	// per-read idle timeout set just below.
	pieceDeadline := time.Now().Add(pieceTimeout)

	for downloaded < work.Length {
		if time.Now().After(pieceDeadline) {
			return nil, fmt.Errorf("download: piece %d timed out", work.Index)
		}

		// Top up the backlog before reading. If we're choked, or the
		// backlog is already full, or there's nothing left to request,
		// this loop does nothing and we fall straight through to the read.
		for !conn.PeerChoking && backlog < backlogLimit && nextBlock < numBlocks {
			offset, length, err := piece.BlockBounds(0, nextBlock, 1, work.Length, work.Length)
			if err != nil {
				return nil, fmt.Errorf("download: piece %d: %w", work.Index, err)
			}
			if err := conn.SendRequest(work.Index, int(offset), int(length)); err != nil {
				return nil, fmt.Errorf("download: piece %d: request block %d: %w", work.Index, nextBlock, err)
			}
			backlog++
			nextBlock++
		}

		// Every read gets a fresh readTimeout window - as long as blocks
		// keep arriving, this deadline keeps getting pushed out, and only
		// a genuinely stalled peer trips it.
		if err := conn.SetIODeadline(readTimeout); err != nil {
			return nil, fmt.Errorf("download: piece %d: %w", work.Index, err)
		}
		msg, err := conn.ReadMessage()
		if err != nil {
			return nil, fmt.Errorf("download: piece %d: read timeout: %w", work.Index, err)
		}

		switch msg.ID {
		case peer.MsgPiece:
			p, err := peer.ParsePiecePayload(msg.Payload, pieceCount, int(work.Length))
			if err != nil {
				return nil, fmt.Errorf("download: piece %d: %w", work.Index, err)
			}
			if p.Index != work.Index {
				continue // stray block from an unrelated piece, ignore
			}
			copy(buf[p.Begin:], p.Block)
			downloaded += int64(len(p.Block))
			progress.AddBytes(len(p.Block))
			backlog--

		case peer.MsgChoke:
			// In-flight requests are silently dropped by a choking peer. Reset backlog and rewind nextBlock to
			// what's actually downloaded, or we'll wait forever for responses that are never coming.
			conn.PeerChoking = true
			backlog = 0
			nextBlock = int(downloaded / piece.BlockSize)

		case peer.MsgUnchoke:
			conn.PeerChoking = false

		case peer.MsgHave:
			if h, err := peer.ParseHavePayload(msg.Payload, pieceCount); err == nil {
				conn.PeerBitfield.SetPiece(h.Index)
			}
		}
	}

	return buf, nil
}
