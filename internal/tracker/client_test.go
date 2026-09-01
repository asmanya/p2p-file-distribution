package tracker

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A well-formed response with one compact peer decodes successfully.
func TestClientAnnounceValid(t *testing.T) {
	body := "d8:intervali1800e5:peers6:\x01\x02\x03\x04\x1a\xe1e"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(body))
	}))
	defer server.Close()

	c := NewClient()
	resp, err := c.Announce(server.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Peers) != 1 {
		t.Fatalf("expected 1 peer, got %d", len(resp.Peers))
	}
}

// A tracker that returns a "failure reason" key must produce an error, not peers.
func TestClientAnnounceFailureReason(t *testing.T) {
	body := "d14:failure reason9:not found e"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(body))
	}))
	defer server.Close()

	c := NewClient()
	if _, err := c.Announce(server.URL); err == nil {
		t.Fatalf("expected error for failure reason")
	}
}

// A body that isn't valid bencode must error, never panic.
func TestClientAnnounceGarbageBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("not bencode at all"))
	}))
	defer server.Close()

	c := NewClient()
	if _, err := c.Announce(server.URL); err == nil {
		t.Fatalf("expected error for garbage body")
	}
}

// A non-200 status must error without attempting to parse the body (often an HTML error page).
func TestClientAnnounceNon200(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	c := NewClient()
	if _, err := c.Announce(server.URL); err == nil {
		t.Fatalf("expected error for non-200 status")
	}
}

// A server slower than the client's timeout must trigger a timeout error, not hang.
func TestClientAnnounceTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		time.Sleep(50 * time.Millisecond)
	}))
	defer server.Close()

	c := &Client{httpClient: &http.Client{Timeout: 10 * time.Millisecond}}
	if _, err := c.Announce(server.URL); err == nil {
		t.Fatalf("expected timeour error")
	}
}

// A body bigger than maxResponseSize must be rejected, not read fully into memory.
func TestClientAnnounceHugeBody(t *testing.T) {
	huge := strings.Repeat("a", maxResponseSize+1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(huge))
	}))
	defer server.Close()

	c := NewClient()
	if _, err := c.Announce(server.URL); err == nil {
		t.Fatalf("expected error for oversized body")
	}
}
