package download

import (
	"net/netip"
	"testing"
	"time"
)

func TestRateTrackerUnknownPeerReturnsZero(t *testing.T) {
	rt := NewRateTracker()
	addr := netip.MustParseAddrPort("127.0.0.1:1")

	if got := rt.DownloadRate(addr); got != 0 {
		t.Errorf("DownloadRate for an unknown peer = %v, want 0", got)
	}
	if got := rt.UploadRate(addr); got != 0 {
		t.Errorf("UploadRate for an unknown peer = %v, want 0", got)
	}
}

func TestRateTrackerComputesDownloadRate(t *testing.T) {
	rt := NewRateTracker()
	addr := netip.MustParseAddrPort("127.0.0.1:1")

	rt.AddDownloaded(addr, 1000)
	// Backdate the first sample so there's a real elapsed interval to compute a rate over - two samples taken
	// back-to-back in a test would otherwise divide by an elapsed time close to zero.
	rt.mu.Lock()
	rt.peers[addr].downSamples[0].at = time.Now().Add(-2 * time.Second)
	rt.mu.Unlock()
	rt.AddDownloaded(addr, 1000)

	got := rt.DownloadRate(addr)
	// ~2000 bytes over ~2 seconds -> ~1000 bytes/sec. Generous bounds to absorb test scheduling jitter.
	if got < 400 || got > 2000 {
		t.Errorf("DownloadRate = %v, want roughly 1000", got)
	}
}

func TestRateTrackerTracksDirectionsIndependently(t *testing.T) {
	rt := NewRateTracker()
	addr := netip.MustParseAddrPort("127.0.0.1:1")

	rt.AddDownloaded(addr, 5000)
	rt.mu.Lock()
	rt.peers[addr].downSamples[0].at = time.Now().Add(-2 * time.Second)
	rt.mu.Unlock()
	rt.AddDownloaded(addr, 5000) // second sample, so there's a nonzero delta to compute a rate over

	// No upload ever recorded for this peer - its upload rate must stay zero regardless of download activity.
	if got := rt.UploadRate(addr); got != 0 {
		t.Errorf("UploadRate = %v, want 0 (nothing uploaded to this peer)", got)
	}
	if got := rt.DownloadRate(addr); got <= 0 {
		t.Errorf("DownloadRate = %v, want > 0", got)
	}
}

func TestRateTrackerStaleWindowReturnsZero(t *testing.T) {
	rt := NewRateTracker()
	addr := netip.MustParseAddrPort("127.0.0.1:1")

	rt.AddDownloaded(addr, 1000)
	rt.AddDownloaded(addr, 1000)

	// Push both samples outside rateSampleWindow - a peer that was fast a while ago but has gone silent since
	// must report 0, not its last-known rate.
	rt.mu.Lock()
	for i := range rt.peers[addr].downSamples {
		rt.peers[addr].downSamples[i].at = time.Now().Add(-2 * rateSampleWindow)
	}
	rt.mu.Unlock()

	if got := rt.DownloadRate(addr); got != 0 {
		t.Errorf("DownloadRate = %v, want 0 for a stale window", got)
	}
}

func TestRateTrackerRemoveClearsPeer(t *testing.T) {
	rt := NewRateTracker()
	addr := netip.MustParseAddrPort("127.0.0.1:1")

	rt.AddDownloaded(addr, 1000)
	rt.AddUploaded(addr, 1000)
	rt.Remove(addr)

	rt.mu.Lock()
	_, tracked := rt.peers[addr]
	rt.mu.Unlock()
	if tracked {
		t.Error("expected peer to be removed from the tracker")
	}
}
