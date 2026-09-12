package download

import (
	"context"
	"log/slog"
	"net/netip"
	"runtime/debug"
	"time"

	"github.com/asmanya/p2p-file-distribution/internal/peer"
	"github.com/asmanya/p2p-file-distribution/internal/piece"
	"github.com/asmanya/p2p-file-distribution/internal/storage"
)

// shutdownGracePeriod is how long Download's main loop keeps servicing resultCh and the tracker after a caller
// asks it to stop, before it actually force-closes every connection - see the callerDone/shutdownDeadline
// handling in download.go. A var, not a const, so tests can shrink it instead of waiting out the real duration.
var shutdownGracePeriod = 5 * time.Second

// SeedConfig groups the read-only state a connection needs to serve incoming block requests: how the torrent is
// laid out and what we currently have on disk. A connection with a nil SeedConfig never serves requests -
// serveRequest is simply never called for it - which is fine for tests that don't care about the upload side.
type SeedConfig struct {
	PieceLength, TotalLength int64
	Have                     *HaveBitfield
	File                     *storage.File
}

// session is one connection's wiring: the peer it talks to, and everything needed to react to whatever arrives
// on it. It exists so inbound messages are handled in exactly one place - an earlier version handled them in two
// (the idle loop and the in-flight piece loop), and the second one silently dropped every request and every
// choke command that arrived while a piece was downloading.
//
// Owned by exactly one goroutine, the connection's own, same as the *peer.Conn inside it.
type session struct {
	conn       *peer.Conn
	addr       netip.AddrPort
	pieceCount int
	progress   *Progress
	rates      *RateTracker
	seed       *SeedConfig
	sendEvent  func(Event) bool
}

// readLoop is a connection's dedicated reader: its only job is turning blocking conn.ReadMessage() calls into
// channel sends, so the connection's main select loop can watch both the coordinator's commands and incoming
// wire messages at once, instead of being stuck inside a direct blocking read. It closes messages when the
// connection dies - a read error, or a peer that's gone silent past readTimeout.
//
// done is what keeps this goroutine from outliving its connection: closing the socket doesn't unblock a
// goroutine already parked on a channel send, so without this select a reader holding one last message would
// stay blocked forever, leaking a goroutine per finished connection.
func readLoop(conn *peer.Conn, messages chan<- peer.Message, done <-chan struct{}) {
	defer close(messages)
	for {
		if err := conn.SetReadDeadline(readTimeout); err != nil {
			return
		}
		msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		select {
		case messages <- msg:
		case <-done:
			return
		}
	}
}

// worker dials addr, completes the initiator side of the handshake, and hands the resulting connection to
// runConnection. Used for peers this client discovered from the tracker and connected to itself.
//
// A panic anywhere in this function or runConnection is recovered, not left to crash the whole program - a single
// malicious or buggy peer shouldn't be able to take down every other in-progress connection with it.
func worker(ctx context.Context, addr netip.AddrPort, infoHash, peerID [20]byte, pieceCount int, coordinator *Coordinator, progress *Progress, rates *RateTracker, seed *SeedConfig) {
	defer func() {
		if r := recover(); r != nil {
			progress.PanicRecovered()
			slog.Error("worker: recovered from panic", "peer", addr, "panic", r, "stack", string(debug.Stack()))
		}
	}()

	progress.ConnectAttempted()
	conn, err := peer.Dial(addr.String(), infoHash, peerID)
	if err != nil {
		slog.Debug("worker: dial failed", "peer", addr, "error", err) // expected - most tracker-returned peers are dead
		return
	}
	progress.ConnectSucceeded()

	runConnection(ctx, conn, addr, pieceCount, coordinator, progress, rates, seed)
}

// serveIncoming hands an already-accepted, already-handshaken connection to runConnection - the responder side of
// the handshake has already run by the time this is called, so there's no dialing to do here.
func serveIncoming(ctx context.Context, conn *peer.Conn, addr netip.AddrPort, pieceCount int, coordinator *Coordinator, progress *Progress, rates *RateTracker, seed *SeedConfig) {
	defer func() {
		if r := recover(); r != nil {
			progress.PanicRecovered()
			slog.Error("serveIncoming: recovered from panic", "peer", addr, "panic", r, "stack", string(debug.Stack()))
		}
	}()
	runConnection(ctx, conn, addr, pieceCount, coordinator, progress, rates, seed)
}

// runConnection drives one already-handshaken connection, in either direction, entirely through events sent to
// and commands received from the coordinator - it never decides which piece to download next, only executes what
// it's told, reports what happens, and serves whatever incoming block requests seed allows.
//
// A single TCP connection is bidirectional after the handshake: whichever side dialed, either peer can request
// pieces from the other, so this loop makes no distinction between "outgoing" and "incoming" once it starts.
//
// Nothing here blocks waiting for the peer to unchoke us. That wait used to gate the whole loop, which meant a
// peer that connected purely to download from us - and so had no reason to ever unchoke us - was dropped after
// the timeout without being served a single block. Interest is declared once, up front, and the coordinator is
// told when the peer's choke state changes; work arrives later, as a command, if and when it makes sense.
func runConnection(ctx context.Context, conn *peer.Conn, addr netip.AddrPort, pieceCount int, coordinator *Coordinator, progress *Progress, rates *RateTracker, seed *SeedConfig) {
	defer conn.Close()
	progress.PeerConnected()
	defer progress.PeerDisconnected()
	slog.Info("peer: connected", "addr", addr)
	defer slog.Info("peer: disconnected", "addr", addr)

	events := coordinator.Events()
	sendEvent := func(ev Event) bool {
		select {
		case events <- ev:
			return true
		case <-ctx.Done():
			return false
		}
	}

	s := &session{
		conn:       conn,
		addr:       addr,
		pieceCount: pieceCount,
		progress:   progress,
		rates:      rates,
		seed:       seed,
		sendEvent:  sendEvent,
	}

	// connDone unblocks both the cancellation watcher and readLoop the moment this function returns, whatever
	// the reason.
	connDone := make(chan struct{})
	defer close(connDone)

	// Force any blocked read to return immediately on cancellation - readLoop has no idea ctx exists, it only
	// blocks on conn.ReadMessage().
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-connDone:
		}
	}()

	commands := make(chan Command, 1)
	if !sendEvent(PeerJoined{Addr: addr, Commands: commands}) {
		return
	}
	defer sendEvent(PeerLeft{Addr: addr})

	conn.PeerBitfield = peerBitfieldFor(pieceCount)

	messages := make(chan peer.Message)
	go readLoop(conn, messages, connDone)

	// Tell the peer what we have before anything else. Without this a peer assumes we have nothing at all, never
	// declares interest, and never asks us for a block - which is exactly what "seeding silently does nothing"
	// looks like from the outside.
	if seed != nil && seed.Have != nil {
		if seed.Have.Count() > 0 {
			if err := conn.SendBitfield(seed.Have.Snapshot()); err != nil {
				return
			}
		}
		// Only ask for data if there's still data we need. A client that already has every piece declaring
		// interest to every peer it meets is just noise on the wire.
		if seed.Have.Count() < pieceCount {
			if err := conn.SendInterested(); err != nil {
				return
			}
			conn.AmInterested = true
		}
	} else {
		if err := conn.SendInterested(); err != nil {
			return
		}
		conn.AmInterested = true
	}

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
				data, err := Piece(s, work, messages, commands)
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

			default:
				if !s.applyCommand(cmd) {
					return
				}
			}

		case msg, ok := <-messages:
			if !ok {
				return // reader goroutine's connection died
			}
			if !s.handleMessage(msg) {
				return
			}
		}
	}
}

// applyCommand executes the commands that mean the same thing whether or not a piece is in flight - the choke
// state we're imposing on this peer. Returns false if the connection should be torn down.
func (s *session) applyCommand(cmd Command) bool {
	switch cmd.(type) {
	case ChokePeer:
		if err := s.conn.SendChoke(); err != nil {
			return false
		}
		s.conn.AmChoking = true
	case UnchokePeer:
		if err := s.conn.SendUnchoke(); err != nil {
			return false
		}
		s.conn.AmChoking = false
	}
	return true
}

// handleMessage reacts to one inbound message that isn't part of an in-flight piece download, reporting anything
// the coordinator needs to know and serving anything the peer asked us for. Returns false if the connection
// should be torn down.
func (s *session) handleMessage(msg peer.Message) bool {
	slog.Debug("peer: message received", "addr", s.addr, "type", msg.ID)

	switch msg.ID {
	case peer.MsgBitfield:
		if err := peer.Validate(peer.Bitfield(msg.Payload), s.pieceCount); err != nil {
			slog.Warn("peer: invalid bitfield, closing connection", "addr", s.addr, "error", err)
			return false // a bitfield that doesn't match this torrent's geometry is a protocol error
		}
		s.conn.PeerBitfield = peer.Bitfield(msg.Payload)
		return s.sendEvent(BitfieldReceived{Addr: s.addr, Bitfield: s.conn.PeerBitfield})

	case peer.MsgHave:
		h, err := peer.ParseHavePayload(msg.Payload, s.pieceCount)
		if err != nil {
			slog.Warn("peer: out-of-range have, ignoring", "addr", s.addr, "error", err)
			return true // out-of-range have from a buggy peer: ignore the message, keep the connection
		}
		s.conn.PeerBitfield.SetPiece(h.Index)
		return s.sendEvent(HaveReceived{Addr: s.addr, Index: h.Index})

	case peer.MsgChoke:
		s.conn.PeerChoking = true
		return s.sendEvent(ChokeReceived{Addr: s.addr})

	case peer.MsgUnchoke:
		s.conn.PeerChoking = false
		return s.sendEvent(UnchokeReceived{Addr: s.addr})

	case peer.MsgInterested:
		s.conn.PeerInterested = true
		return s.sendEvent(InterestedReceived{Addr: s.addr})

	case peer.MsgNotInterested:
		s.conn.PeerInterested = false
		return s.sendEvent(NotInterestedReceived{Addr: s.addr})

	case peer.MsgRequest:
		if s.seed == nil {
			return true
		}
		err := serveRequest(s.conn, s.addr, msg.Payload, s.pieceCount, s.seed.PieceLength, s.seed.TotalLength, s.seed.Have, s.seed.File, s.progress, s.rates)
		if err != nil {
			slog.Warn("peer: bad request, closing connection", "addr", s.addr, "error", err)
		}
		return err == nil // malformed or oversized request - this peer is misbehaving, not worth keeping open

	case peer.MsgCancel:
		// Requests are served synchronously, one at a time, so by the time a cancel arrives its block has
		// already been sent or was never queued. Nothing to undo.
		return true
	}
	return true
}

// peerBitfieldFor returns a zeroed bitfield sized for pieceCount. A peer that hasn't sent one yet counts as
// having nothing - and a correctly sized empty one means a "have" arriving before any bitfield still lands in a
// slot that exists, instead of being silently dropped on a nil slice.
func peerBitfieldFor(pieceCount int) peer.Bitfield {
	return make(peer.Bitfield, (pieceCount+7)/8)
}
