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

// readLoop is a worker's dedicated connection reader: its only job is turning blocking conn.ReadMessage() calls
// into channel sends, so worker's main select loop can watch both the coordinator's commands and incoming wire
// messages at once, instead of being stuck inside a direct blocking read. It closes messages when the connection
// dies - a read error, or a peer that's gone silent past readTimeout.
func readLoop(conn *peer.Conn, messages chan<- peer.Message) {
	defer close(messages)
	for {
		if err := conn.SetIODeadline(readTimeout); err != nil {
			return
		}
		msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		messages <- msg
	}
}

// worker connects to one peer and drives it entirely through events sent to, and commands received from, the
// coordinator - it never decides which piece to download next, only executes what it's told and reports what
// happens.
//
// A panic anywhere in this function is recovered, not left to crash the whole program - a single malicious or
// buggy peer shouldn't be able to take down every other in-progress connection with it.
func worker(ctx context.Context, addr netip.AddrPort, infoHash, peerID [20]byte, pieceCount int, coordinator *Coordinator, progress *Progress) {
	defer func() {
		if r := recover(); r != nil {
			progress.PanicRecovered()
			slog.Error("worker: recovered from panic", "peer", addr, "panic", r, "stack", string(debug.Stack()))
		}
	}()

	events := coordinator.Events()
	sendEvent := func(ev Event) bool {
		select {
		case events <- ev:
			return true
		case <-ctx.Done():
			return false
		}
	}

	progress.ConnectAttempted()
	conn, err := peer.Dial(addr.String(), infoHash, peerID)
	if err != nil {
		return // dead peer - expected, nothing to log loudly about here
	}
	progress.ConnectSucceeded()
	defer conn.Close()
	progress.PeerConnected()
	defer progress.PeerDisconnected()

	// Force any blocked read to return immediately on cancellation - readLoop has no idea ctx exists, it only
	// blocks on conn.ReadMessage().
	watchDone := make(chan struct{})
	defer close(watchDone)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-watchDone:
		}
	}()

	commands := make(chan Command, 1)
	if !sendEvent(PeerJoined{Addr: addr, Commands: commands}) {
		return
	}
	defer sendEvent(PeerLeft{Addr: addr})

	messages := make(chan peer.Message)
	go readLoop(conn, messages)

	bf, err := receiveBitfield(messages, pieceCount)
	if err != nil {
		return
	}
	if bf != nil {
		conn.PeerBitfield = bf
		if !sendEvent(BitfieldReceived{Addr: addr, Bitfield: bf}) {
			return
		}
	}

	if err := EnsureUnchoked(conn, messages); err != nil {
		return
	}

	if !sendEvent(PeerReady{Addr: addr}) {
		return
	}

	onHave := func(index int) { sendEvent(HaveReceived{Addr: addr, Index: index}) }

	for {
		select {
		case <-ctx.Done():
			return

		case cmd, ok := <-commands:
			if !ok {
				return
			}
			switch c := cmd.(type) {
			case AssignPiece:
				work := piece.Work{Index: c.Index, ExpectedHash: c.ExpectedHash, Length: c.Length}
				data, err := Piece(conn, work, pieceCount, progress, messages, commands, onHave)
				switch {
				case err == errCancelled:
					// abandoned mid-flight - the coordinator already knows someone else finished this piece
				case err == errShutdown:
					return
				case err != nil:
					sendEvent(PieceFailed{Addr: addr, Index: c.Index, Reason: err})
					return // this connection is suspect - let another worker take over
				default:
					_ = conn.SendHave(c.Index) // best-effort courtesy
					if !sendEvent(PieceDownloaded{Addr: addr, Index: c.Index, Data: data}) {
						return
					}
				}
				if !sendEvent(PeerReady{Addr: addr}) {
					return
				}

			case CancelPiece:
				// nothing in flight right now - a cancel that arrived after we'd already finished or given up

			case Pause:
				// nothing to do, just keep waiting for the next command

			case Shutdown:
				return
			}

		case msg, ok := <-messages:
			if !ok {
				return // reader goroutine's connection died
			}
			switch msg.ID {
			case peer.MsgHave:
				if h, err := peer.ParseHavePayload(msg.Payload, pieceCount); err == nil {
					conn.PeerBitfield.SetPiece(h.Index)
					if !sendEvent(HaveReceived{Addr: addr, Index: h.Index}) {
						return
					}
				}
			case peer.MsgChoke:
				conn.PeerChoking = true
			case peer.MsgUnchoke:
				conn.PeerChoking = false
			}
		}
	}
}

// receiveBitfield waits briefly for the peer's bitfield, which - if sent at all - is conventionally the first message
// after a handshake. A peer with zero pieces may skip it entirely, so a timeout or an early non-bitfield message
// here is treated as "no pieces yet", not a fatal error.
func receiveBitfield(messages <-chan peer.Message, pieceCount int) (peer.Bitfield, error) {
	select {
	case msg, ok := <-messages:
		if !ok || msg.ID != peer.MsgBitfield {
			return nil, nil
		}
		if err := peer.Validate(peer.Bitfield(msg.Payload), pieceCount); err != nil {
			return nil, fmt.Errorf("download: %w", err)
		}
		return peer.Bitfield(msg.Payload), nil
	case <-time.After(bitfieldWaitTimeout):
		return nil, nil
	}
}
