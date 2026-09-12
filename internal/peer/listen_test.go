package peer

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// dialLoopback connects to l's bound address over real TCP loopback.
func dialLoopback(t *testing.T, l *Listener) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatalf("dial %s: %v", l.Addr(), err)
	}
	return conn
}

func TestListenerAcceptsAndHandles(t *testing.T) {
	l, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer l.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var handled atomic.Bool
	done := make(chan struct{})
	go func() {
		l.Serve(ctx, func(conn net.Conn) {
			handled.Store(true)
			conn.Close()
			close(done)
		})
	}()

	client := dialLoopback(t, l)
	defer client.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handle was never called")
	}

	if !handled.Load() {
		t.Error("expected handle to be called")
	}
}

func TestListenerEnforcesMaxConnections(t *testing.T) {
	origMax := MaxIncomingConnections
	MaxIncomingConnections = 1
	defer func() { MaxIncomingConnections = origMax }()

	l, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer l.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	release := make(chan struct{})
	var handledCount atomic.Int32
	go func() {
		l.Serve(ctx, func(conn net.Conn) {
			handledCount.Add(1)
			<-release // hold the first connection open so the slot stays taken
			conn.Close()
		})
	}()

	first := dialLoopback(t, l)
	defer first.Close()

	// Give the accept loop time to admit the first connection before the second arrives.
	time.Sleep(100 * time.Millisecond)

	second := dialLoopback(t, l)
	defer second.Close()

	// The second connection should be rejected (closed by the server) without ever reaching handle.
	buf := make([]byte, 1)
	second.SetReadDeadline(time.Now().Add(time.Second))
	_, err = second.Read(buf)
	if err == nil {
		t.Fatal("expected the second connection to be closed by the server, got a live read")
	}

	close(release)
	time.Sleep(50 * time.Millisecond)
	if got := handledCount.Load(); got != 1 {
		t.Errorf("handle called %d times, want exactly 1", got)
	}
}

func TestListenerEnforcesPerIPLimit(t *testing.T) {
	origPerIP := MaxConnectionsPerIP
	MaxConnectionsPerIP = 1
	defer func() { MaxConnectionsPerIP = origPerIP }()

	l, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer l.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	var handledAddrs []string
	release := make(chan struct{})
	go func() {
		l.Serve(ctx, func(conn net.Conn) {
			mu.Lock()
			handledAddrs = append(handledAddrs, conn.RemoteAddr().String())
			mu.Unlock()
			<-release
			conn.Close()
		})
	}()

	first := dialLoopback(t, l)
	defer first.Close()
	time.Sleep(100 * time.Millisecond)

	// Same remote IP (127.0.0.1) as first - should be rejected even though MaxIncomingConnections is nowhere near hit.
	second := dialLoopback(t, l)
	defer second.Close()

	buf := make([]byte, 1)
	second.SetReadDeadline(time.Now().Add(time.Second))
	_, err = second.Read(buf)
	if err == nil {
		t.Fatal("expected the second connection from the same IP to be rejected")
	}

	close(release)
	mu.Lock()
	defer mu.Unlock()
	if len(handledAddrs) != 1 {
		t.Errorf("handle called for %d connections, want exactly 1", len(handledAddrs))
	}
}

func TestListenerStopsOnContextCancel(t *testing.T) {
	l, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer l.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		l.Serve(ctx, func(net.Conn) {})
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after context cancellation")
	}
}

func TestAcceptSuccessfulHandshake(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	infoHash := [20]byte{1}
	clientPeerID := [20]byte{2}
	serverPeerID := [20]byte{3}

	done := make(chan struct{})
	go func() {
		defer close(done)
		ours := Handshake{InfoHash: infoHash, PeerID: clientPeerID}
		if _, err := client.Write(ours.Serialize()); err != nil {
			t.Errorf("client write handshake: %v", err)
			return
		}
		theirs, err := ParseHandshake(client)
		if err != nil {
			t.Errorf("client read handshake: %v", err)
			return
		}
		if theirs.PeerID != serverPeerID {
			t.Errorf("client saw peer ID %x, want %x", theirs.PeerID, serverPeerID)
		}
	}()

	conn, err := Accept(server, infoHash, serverPeerID)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if conn.PeerID != clientPeerID {
		t.Errorf("Accept returned peer ID %x, want %x", conn.PeerID, clientPeerID)
	}

	<-done
}

func TestAcceptInfoHashMismatch(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	go func() {
		ours := Handshake{InfoHash: [20]byte{0xAA}, PeerID: [20]byte{2}}
		client.Write(ours.Serialize())
	}()

	_, err := Accept(server, [20]byte{0xBB}, [20]byte{3})
	if !errors.Is(err, ErrInfoHashMismatch) {
		t.Fatalf("Accept error = %v, want ErrInfoHashMismatch", err)
	}
}

func TestAcceptTimesOutOnSilentPeer(t *testing.T) {
	orig := acceptHandshakeTimeout
	acceptHandshakeTimeout = 50 * time.Millisecond
	defer func() { acceptHandshakeTimeout = orig }()

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	_, err := Accept(server, [20]byte{1}, [20]byte{2})
	if !errors.Is(err, ErrHandshakeTimeout) {
		t.Fatalf("Accept error = %v, want ErrHandshakeTimeout", err)
	}
}
