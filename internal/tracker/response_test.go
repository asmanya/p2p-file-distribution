package tracker

import (
	"net/netip"
	"testing"
)

func TestParseAnnounceResponseCompactPeers(t *testing.T) {
	body := []byte("d8:intervali1800e5:peers6:\x01\x02\x03\x04\x1a\xe1e")

	resp, err := ParseAnnounceResponse(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Interval != 1800 {
		t.Errorf("interval = %d, want 1800", resp.Interval)
	}

	// The 6-byte peer blob \x01\x02\x03\x04\x1a\xe1 decodes to IP 1.2.3.4
	// (first 4 bytes) and port 0x1ae1 = 6881 (last 2 bytes, big-endian).
	want := netip.AddrPortFrom(netip.AddrFrom4([4]byte{1, 2, 3, 4}), 0x1ae1)
	if len(resp.Peers) != 1 || resp.Peers[0] != want {
		t.Errorf("peers = %v, want [%v]", resp.Peers, want)
	}
}

func TestParseAnnounceResponseFailureReason(t *testing.T) {
	body := []byte("d14:failure reason22:torrent not registerede")

	_, err := ParseAnnounceResponse(body)
	if err == nil {
		t.Fatalf("expected an error for failure reason, got nil")
	}
}

func TestParseAnnounceResponseInvalidPeersLength(t *testing.T) {
	body := []byte("d5:peers5:AAAAAe")

	_, err := ParseAnnounceResponse(body)
	if err == nil {
		t.Fatalf("expected an error for malformed peers blob, got nil")
	}
}
