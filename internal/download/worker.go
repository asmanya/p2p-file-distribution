package download

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"runtime/debug"
	"time"

	"github.com/asmanya/p2p-file-distribution/internal/peer"
	"github.com/asmanya/p2p-file-distribution/internal/piece"
)

const bitfieldWaitTimeout = 10 * time.Second

// requestBackoff is a small pause before a worker goes back to the queue after finding out the peer it's connected to
// doesn't have the piece it just grabbed. Without it, a peer missing a piece the rest of the swarm also lacks turns
// into a tight requeue/grab/requeue loop across every worker holding that connection - all CPU, no progress.
const requeueBackoff = 50 * time.Millisecond

// worker connects to one peer and downloads pieces from workCh until the queue is drained, the context is cancelled,
// or the connection fails. Every piece it can't complete goes back onto workCh before it exits, so another
// worker can pick it up from a healthier peer.
//
// A panic anywhere in this function is recovered, not left to crash the whole program - a single malicious or buggy
// peer shouldn't be able to take down every other in-progress connection with it. The recovery is loud (full stack
// trace at error level) precisely so it never quietly hides a real bug during development.
func worker(ctx context.Context, addr netip.AddrPort, infoHash, peerID [20]byte, pieceCount int, workCh chan piece.Work, resultCh chan Result, progress *Progress) {
	var current *piece.Work
	defer func() {
		if r := recover(); r != nil {
			progress.PanicRecovered()
			slog.Error("worker: recovered from panic", "peer", addr, "panic", r, "stack", string(debug.Stack()))
			if current != nil {
				workCh <- *current
			}
		}
	}()

	progress.ConnectAttempted()
	conn, err := peer.Dial(addr.String(), infoHash, peerID)
	if err != nil {
		return // dead-peer - expected, nothing to log loudly about here
	}
	progress.ConnectSucceeded()
	defer conn.Close()
	progress.PeerConnected()
	defer progress.PeerDisconnected()

	// Piece(), EnsureUnchoked(), and friends only know about read/write
	// deadlines measured in seconds - they have no idea ctx exists. Rather
	// than threading ctx through every one of them, this goroutine watches
	// for cancellation and forces the connection closed the moment it
	// happens, which makes any Read or Write currently blocked on it return
	// immediately with an error instead of waiting out its own timeout.
	watchDone := make(chan struct{})
	defer close(watchDone)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-watchDone:
		}
	}()

	if err := receiveBitfield(conn, pieceCount); err != nil {
		return
	}

	if err := EnsureUnchoked(conn); err != nil {
		return
	}

	for {
		// ctx.Done() sits in the same select as the channel read, not in a
		// separate check beforehand - a lone "check, then maybe block on
		// workCh" would miss a cancellation that arrives while this worker
		// is sitting idle waiting for a piece that never comes.
		var work piece.Work
		var ok bool
		select {
		case <-ctx.Done():
			return
		case work, ok = <-workCh:
			if !ok {
				return // queue closed - download is complete or shutting down
			}
		}
		current = &work

		if conn.PeerBitfield != nil && !conn.PeerBitfield.HasPiece(work.Index) {
			workCh <- work
			current = nil
			time.Sleep(requeueBackoff)
			continue
		}

		data, err := Piece(conn, work, pieceCount, progress)
		if err != nil {
			workCh <- work
			return // this connection is suspect - let another worker take over
		}
		current = nil // downloaded and verified - no longer at risk of being lost to a panic

		// best-effort courtesy notice - failing to send it doesn't
		// invalidate the piece we already downloaded and verified
		_ = conn.SendHave(work.Index)

		// A plain, unconditional send here would risk hanging forever: resultCh's buffer (queue.go) is a small fixed
		// size, not one slot per piece like workCh's is, so it offers no such guarantee. If Download() has already
		// taken its ctx.Done() shutdown path, nothing is left to drain this channel - without the ctx.Done() case
		// below, a worker finishing at exactly the wrong moment would block here forever, and wg.Wait() in Download()
		// would never return.
		select {
		case resultCh <- Result{Index: work.Index, Data: data}:
		case <-ctx.Done():
			return
		}
	}
}

// receiveBitfield waits briefly for the peer's bitfield, which - if sent at all - is conventionally the first message
// after a handshake. A peer with zero pieces may skip it entirely, so a timeout or an early non-bitfield message
// here is treated as "no pieces yet", not a fatal error.
func receiveBitfield(conn *peer.Conn, pieceCount int) error {
	if err := conn.SetIODeadline(bitfieldWaitTimeout); err != nil {
		return err
	}
	msg, err := conn.ReadMessage()
	if err != nil {
		return nil
	}
	if msg.ID != peer.MsgBitfield {
		return nil
	}
	if err := peer.Validate(peer.Bitfield(msg.Payload), pieceCount); err != nil {
		return fmt.Errorf("download: %w", err)
	}

	conn.PeerBitfield = peer.Bitfield(msg.Payload)
	return nil
}
