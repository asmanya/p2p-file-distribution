package peer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"time"
)

// MaxIncomingConnections and MaxConnectionsPerIP are vars, not consts, so tests can shrink them temporarily instead
// of opening hundreds of real sockets to hit the limit.
var (
	// MaxIncomingConnections caps total simultaneous incoming peers. Without a cap, an unbounded number of connections exhausts
	// file descriptors and starves our own outgoing dials.
	MaxIncomingConnections = 200

	// MaxConnectionsPerPeer caps how many simultaneous connections a single remote IP may hold open. 50 connections from one IP
	// is not a normal swarm participant.
	MaxConnectionsPerIP = 3
)

// acceptErrorBackoff is how long the accept loop pauses after a transient Accept error before retrying, so a run of error
// (e.g. fd exhaustion) can't spin the loop at 100% CPU.
const acceptErrorBackoff = 200 * time.Millisecond

// Listener accepts incoming peer connections on one TCP port, enforcing connection-count guards before handing each one off to
// a handler goroutine.
type Listener struct {
	ln net.Listener // listening socket

	// handlers counts connection handler goroutines Serve has spawned. Incremented inside Serve, before the
	// goroutine starts, so a caller that waits on it after Serve returns can never miss a late arrival - doing
	// the accounting from inside the handler itself would race with that wait.
	handlers sync.WaitGroup

	mu    sync.Mutex
	total int                // no. of active connections
	perIP map[netip.Addr]int // no. of connections to a remote IP
}

// Listen opens a TCP listener on addr (e.g. ":6881").
func Listen(addr string) (*Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("peer: listen %s: %w", addr, err)
	}
	return &Listener{ln: ln, perIP: make(map[netip.Addr]int)}, nil
}

// Addr returns the listener's bound address - useful when addr was ":0" and the OS picked the port.
func (l *Listener) Addr() net.Addr {
	return l.ln.Addr()
}

// Close closes the underlying listener, unblocking any pending Accept.
func (l *Listener) Close() error {
	return l.ln.Close()
}

// Serve run the accept loop until ctx is cancelled. For each accepted connection that passes the connection guards, handle runs in
// its own goroutine, a rejected connection is closed immediately without ever reaching handle.
func (l *Listener) Serve(ctx context.Context, handle func(net.Conn)) {
	go func() {
		<-ctx.Done()
		l.ln.Close()
	}()

	for {
		conn, err := l.ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return // listener closed because ctx was cancelled, not a real failure
			}
			if errors.Is(err, net.ErrClosed) {
				return // someone called Close - retrying would spin on the same error forever
			}
			slog.Warn("peer: accept error, backing off", "error", err)
			time.Sleep(acceptErrorBackoff)
			continue
		}

		if !l.admit(conn) {
			conn.Close()
			continue
		}

		l.handlers.Add(1)
		go func() {
			defer l.handlers.Done()
			defer l.release(conn)
			handle(conn)
		}()
	}
}

// Wait blocks until every handler goroutine Serve started has returned. Only meaningful after Serve itself has
// returned - at that point no new handlers can appear, so this is the point where all accepted connections are
// known to be finished with.
func (l *Listener) Wait() {
	l.handlers.Wait()
}

// admit applies the connection-count guards and, if the connection is accepted, reservers its slot. Returns false if conn should be rejected.
func (l *Listener) admit(conn net.Conn) bool {
	ip := remoteIP(conn)

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.total >= MaxIncomingConnections {
		return false
	}
	if l.perIP[ip] >= MaxConnectionsPerIP {
		return false
	}
	l.total++
	l.perIP[ip]++
	return true
}

// release closes conn and frees the slot admit reserved for it.
func (l *Listener) release(conn net.Conn) {
	conn.Close()
	ip := remoteIP(conn)

	l.mu.Lock()
	defer l.mu.Unlock()

	l.total--
	l.perIP[ip]--
	if l.perIP[ip] <= 0 {
		delete(l.perIP, ip)
	}
}

// remoteIP extracts the bare IP from conn's remote address, dropping the port - connections from the same host on different ports
// still count against the same per-IP limit.
func remoteIP(conn net.Conn) netip.Addr {
	addrPort, err := netip.ParseAddrPort(conn.RemoteAddr().String())
	if err != nil {
		return netip.Addr{}
	}
	return addrPort.Addr()
}

// acceptHandshakeTimeout bounds the responder side of the handshake exchange - a peer that opens a connection and then sends
// nothing must not hang this goroutine forever.
var acceptHandshakeTimeout = 5 * time.Second

// Accept performs the responder side of the handshake protocol on an already-accepted connection. Unlike Dial, we don't know
// which torrent the remote peer wants untill it tells us - so, unlike the initiator, we read their handshake first, verify
// infoHash matches what we're serving, and only then send ours back. On a mismatch we return before ever sending our own peer
// ID or torrent info to an unverified connection.
func Accept(conn net.Conn, infoHash, peerID [20]byte) (*Conn, error) {
	if err := conn.SetDeadline(time.Now().Add(acceptHandshakeTimeout)); err != nil {
		return nil, fmt.Errorf("peer: set deadline: %w", err)
	}

	theirs, err := ParseHandshake(conn)
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return nil, fmt.Errorf("peer: %s: %w", conn.RemoteAddr(), ErrHandshakeTimeout)
		}
		return nil, fmt.Errorf("peer: receive handshake: %w", err)
	}

	if theirs.InfoHash != infoHash {
		return nil, fmt.Errorf("peer: %s: %w", conn.RemoteAddr(), ErrInfoHashMismatch)
	}

	ours := Handshake{InfoHash: infoHash, PeerID: peerID}
	if _, err := conn.Write(ours.Serialize()); err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return nil, fmt.Errorf("peer: %s: %w", conn.RemoteAddr(), ErrHandshakeTimeout)
		}
		return nil, fmt.Errorf("peer: send handshake: %w", err)
	}

	// Handshake is done - clear the deadline so it can't fire mid-download later, same as the dialing side.
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return nil, fmt.Errorf("peer: clear deadline: %w", err)
	}

	return newConn(conn, theirs.PeerID, theirs.Reserved), nil
}
