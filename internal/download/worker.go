package download

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"github.com/asmanya/p2p-file-distribution/internal/peer"
	"github.com/asmanya/p2p-file-distribution/internal/piece"
)

const bitfieldWaitTimeout = 10 * time.Second

// worker connects to one peer and downloads pieces from workCh until the queue is drained, the context is cancelled,
// or the connection fails. Every piece it can't complete goes back onto workCh before it exits, so another
// worker can pick it up from a healthier peer.
func worker(ctx context.Context, addr netip.AddrPort, infoHash, peerID [20]byte, pieceCount int, workCh chan piece.Work, resultCh chan Result) {
	conn, err := peer.Dial(addr.String(), infoHash, peerID)
	if err != nil {
		return // dead-peer - expected, nothing to log loudly about here
	}
	defer conn.Close()

	if err := receiveBitfield(conn, pieceCount); err != nil {
		return
	}

	if err := EnsureUnchoked(conn); err != nil {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		work, ok := <-workCh
		if !ok {
			return // queue closed - download is complete or shutting down
		}

		if conn.PeerBitfield != nil && !conn.PeerBitfield.HasPiece(work.Index) {
			workCh <- work
			continue
		}

		data, err := Piece(conn, work, pieceCount)
		if err != nil {
			workCh <- work
			return // this connection is suspect - let another worker take over
		}

		// best-effort courtesy notice - failing to send it doesn't
		// invalidate the piece we already downloaded and verified
		_ = conn.SendHave(work.Index)

		resultCh <- Result{Index: work.Index, Data: data}
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
