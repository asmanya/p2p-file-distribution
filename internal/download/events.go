package download

import (
	"net/netip"

	"github.com/asmanya/p2p-file-distribution/internal/peer"
)

// Event is anything a peer worker reports to the coordinator. The coordinator is the single owner of all shared download
// state - workers never touch that state directly, only describe what happened and let the coordinator decide what it means.
type Event interface {
	isEvent()
}

// PeerJoined announces a new connected peer and hands the coordinator the channel it should send that peer's commands on. The
// worker owns and reads this channel, the coordinator only ever writes to it.
type PeerJoined struct {
	Addr     netip.AddrPort
	Commands chan<- Command
}

// BitfieldReceived reports a peer's full set of available pieces, sent once right after the handshake.
type BitfieldReceived struct {
	Addr     netip.AddrPort
	Bitfield peer.Bitfield
}

// HaveReceived reports one newly available piece, sent whenever the peer finishes downloading it from someone else.
type HaveReceived struct {
	Addr  netip.AddrPort
	Index int
}

// PieceDownloaded reports a piece that finished downloading and passed its SHA-1 check.
type PieceDownloaded struct {
	Addr  netip.AddrPort
	Index int
	Data  []byte
}

// PieceFailed reports a piece that downloaded but failed verification, or that couldn't be downloaded at all.
type PieceFailed struct {
	Addr   netip.AddrPort
	Index  int
	Reason error
}

// PeerReady signals the peer is unchoked and idle, asking the coordinator for something to do.
type PeerReady struct {
	Addr netip.AddrPort
}

// PeerLeft reports a peer's worker exiting, for any reason - connection dropped, protocol error, shutdown.
type PeerLeft struct {
	Addr   netip.AddrPort
	Reason error
}

func (PeerJoined) isEvent()       {}
func (BitfieldReceived) isEvent() {}
func (HaveReceived) isEvent()     {}
func (PieceDownloaded) isEvent()  {}
func (PieceFailed) isEvent()      {}
func (PeerReady) isEvent()        {}
func (PeerLeft) isEvent()         {}

// Command is what the coordinator tells one peer worker to do next
type Command interface {
	isCommand()
}

// AssignPiece assigns one piece to the worker.
type AssignPiece struct {
	Index  int
	Length int64
}

// CancelPiece tells the worker to abandon an in-flight request - used by endgame mode once another peer finishes the same piece first.
type CancelPiece struct {
	Index int
}

// Pause tells the worker there's nothing to assign right now, it should wait for the next command rather than polling the coordinator.
type Pause struct{}

// Shutdown tells the worker to close its connection and exit.
type Shutdown struct{}

func (AssignPiece) isCommand() {}
func (CancelPiece) isCommand() {}
func (Pause) isCommand()       {}
func (Shutdown) isCommand()    {}
