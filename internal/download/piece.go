package download

import (
	"errors"
	"fmt"
	"time"

	"github.com/asmanya/p2p-file-distribution/internal/peer"
	"github.com/asmanya/p2p-file-distribution/internal/piece"
)

// pieceTimeout and readTimeout are vars, not consts, so tests can shrink them temporarily instead of waiting out
// real timeouts.
var (
	pieceTimeout = 30 * time.Second
	readTimeout  = 15 * time.Second
)

const backlogLimit = 5 // TODO: adaptive backlog based on peer speed

var errCancelled = errors.New("download: piece cancelled")
var errShutdown = errors.New("download: shutdown")

// Piece downloads a single piece over s's connection, blocking until the piece is fully assembled and
// hash-verified, or an error occurs.
//
// It does not require the peer to have unchoked us first: if we're choked when it starts, it waits for the
// unchoke that lets it send requests, bounded by pieceTimeout like everything else here. A choke arriving
// mid-download is handled the same way, by re-requesting whatever hadn't arrived yet once the peer relents.
func Piece(s *session, work piece.Work, messages <-chan peer.Message, commands <-chan Command) ([]byte, error) {
	buf, err := downloadBlocks(s, work, messages, commands)
	if err != nil {
		return nil, err
	}

	// Trust nothing until the bytes match the hash from the .torrent file. A peer can lie, corrupt data in
	// transit, or send blocks for the wrong piece entirely - this is the only check that catches all three.
	ok, err := piece.Verify(buf, work.ExpectedHash)
	if err != nil {
		return nil, fmt.Errorf("download: piece %d: %w", work.Index, err)
	}
	if !ok {
		s.progress.HashFailed()
		return nil, fmt.Errorf("download: piece %d: hash mismatch", work.Index)
	}

	return buf, nil
}

// downloadBlocks requests and assembles every block of work, pipelining up to backlogLimit requests at a time so
// round-trip latency overlaps across blocks instead of stacking up one request at a time. pieceTimeout bounds the
// whole call; readTimeout only bounds a single read (in readLoop) and is reset on every read, so a peer that's
// merely slow but still making progress survives, while one that's gone silent gets dropped.
//
// Progress is tracked per block, not as a running byte count: a peer that sends the same block twice would
// otherwise push the count past the piece length while leaving a hole in buf, and a choke arriving after
// out-of-order blocks would leave the re-request cursor pointing past blocks that never arrived. Both show up
// only as an unexplained hash failure or a piece that hangs until it times out.
func downloadBlocks(s *session, work piece.Work, messages <-chan peer.Message, commands <-chan Command) ([]byte, error) {
	conn := s.conn
	buf := make([]byte, work.Length)

	// work.Length is already this piece's resolved (possibly short) length, so BlockCount/BlockBounds are called
	// as if this were a "torrent" of exactly one piece - reusing the tested geometry math instead of re-deriving
	// block-size arithmetic here.
	numBlocks, err := piece.BlockCount(0, 1, work.Length, work.Length)
	if err != nil {
		return nil, fmt.Errorf("download: piece %d: %w", work.Index, err)
	}

	requested := make([]bool, numBlocks)
	received := make([]bool, numBlocks)
	outstanding := 0 // requests sent but not yet answered
	remaining := numBlocks

	// Fixed cap on the whole call - the "give up no matter what" ceiling, separate from the per-read idle
	// timeout readLoop applies. One timer for the call, rather than a fresh time.After on every trip around the
	// loop, which would pile up a live timer per block.
	deadline := time.NewTimer(pieceTimeout)
	defer deadline.Stop()

	for remaining > 0 {
		for !conn.PeerChoking && outstanding < backlogLimit {
			next := nextUnrequestedBlock(requested)
			if next < 0 {
				break // everything's either in flight or already here
			}
			offset, length, err := piece.BlockBounds(0, next, 1, work.Length, work.Length)
			if err != nil {
				return nil, fmt.Errorf("download: piece %d: %w", work.Index, err)
			}
			if err := conn.SendRequest(work.Index, int(offset), int(length)); err != nil {
				return nil, fmt.Errorf("download: piece %d: request block %d: %w", work.Index, next, err)
			}
			requested[next] = true
			outstanding++
		}

		select {
		case msg, ok := <-messages:
			if !ok {
				return nil, fmt.Errorf("download: piece %d: connection closed", work.Index)
			}

			switch msg.ID {
			case peer.MsgPiece:
				p, err := peer.ParsePiecePayload(msg.Payload, s.pieceCount, int(work.Length))
				if err != nil {
					return nil, fmt.Errorf("download: piece %d: %w", work.Index, err)
				}
				if p.Index != work.Index {
					continue // a block for a piece we're not downloading - stale, or meant for someone else
				}
				if p.Begin%piece.BlockSize != 0 {
					continue // unaligned offsets can't be mapped to a block slot; ignore rather than mis-account
				}
				block := p.Begin / piece.BlockSize
				if block < 0 || block >= numBlocks {
					continue
				}
				if outstanding > 0 {
					outstanding--
				}
				if received[block] {
					continue // duplicate: already counted, and counting it again would corrupt the accounting
				}
				copy(buf[p.Begin:], p.Block)
				received[block] = true
				requested[block] = true
				remaining--
				s.progress.AddBytes(len(p.Block))
				s.rates.AddDownloaded(s.addr, len(p.Block))

			case peer.MsgChoke:
				conn.PeerChoking = true
				outstanding = 0
				// Anything we asked for but never got has to be asked for again once they relent - only what
				// actually arrived counts as done.
				for i := range requested {
					requested[i] = received[i]
				}
				if !s.sendEvent(ChokeReceived{Addr: s.addr}) {
					return nil, errShutdown
				}

			case peer.MsgUnchoke:
				conn.PeerChoking = false
				if !s.sendEvent(UnchokeReceived{Addr: s.addr}) {
					return nil, errShutdown
				}

			default:
				// Everything else - their bitfield, haves, interest, and above all the blocks they're asking
				// us for - goes through the same handler the idle loop uses. Dropping these here is what used
				// to make this client stop serving anyone the moment it started downloading.
				if !s.handleMessage(msg) {
					return nil, fmt.Errorf("download: piece %d: connection closed by handler", work.Index)
				}
			}

		case cmd, ok := <-commands:
			if !ok {
				return nil, errShutdown
			}
			switch c := cmd.(type) {
			case CancelPiece:
				if c.Index == work.Index {
					return nil, errCancelled
				}
			case Shutdown:
				return nil, errShutdown
			default:
				// Choke and unchoke decisions must still reach the wire while a piece is in flight - they were
				// being read off this channel and thrown away, so the coordinator believed it had unchoked
				// peers it had never actually told.
				if !s.applyCommand(cmd) {
					return nil, fmt.Errorf("download: piece %d: connection closed applying command", work.Index)
				}
			}

		case <-deadline.C:
			return nil, fmt.Errorf("download: piece %d timed out", work.Index)
		}
	}

	return buf, nil
}

// nextUnrequestedBlock returns the index of the first block not yet asked for, or -1 if every block is either in
// flight or already received.
func nextUnrequestedBlock(requested []bool) int {
	for i, r := range requested {
		if !r {
			return i
		}
	}
	return -1
}
