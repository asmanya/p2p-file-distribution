package tracker

import (
	"net/url"
	"testing"
)

func TestBuildAnnounceURL(t *testing.T) {
	var infoHash, peerID [20]byte
	for i := range infoHash {
		infoHash[i] = byte(i)
	}
	for i := range peerID {
		peerID[i] = byte(i + 100)
	}

	got, err := BuildAnnounceURL("http://tracker.example.com/announce", infoHash, peerID, 6881, 12345)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("produced invalid URL: %v", err)
	}
	q := u.Query()

	if q.Get("port") != "6881" {
		t.Errorf("port = %q, want 6881", q.Get("port"))
	}
	if q.Get("left") != "12345" {
		t.Errorf("left = %q, want 12345", q.Get("left"))
	}
	if q.Get("compact") != "1" {
		t.Errorf("compact = %q, want 1", q.Get("compact"))
	}
	if q.Get("event") != "started" {
		t.Errorf("event = %q, want started", q.Get("event"))
	}
	if q.Get("info_hash") != string(infoHash[:]) {
		t.Errorf("info_hash decoded mismatch")
	}
	if q.Get("peer_id") != string(peerID[:]) {
		t.Errorf("peer_id decoded mismatch")
	}
}
