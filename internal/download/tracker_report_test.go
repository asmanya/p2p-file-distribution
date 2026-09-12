package download

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/asmanya/p2p-file-distribution/internal/tracker"
)

// recordingTrackerServer behaves like fakeTrackerServer (download_test.go) but also records every announce
// request's query parameters, in order, so a test can assert on the sequence of events (started/completed/
// stopped) a real download session actually sends - not just that a single announce URL was built correctly.
func recordingTrackerServer(t *testing.T, addrs []netip.AddrPort) (trackerURL string, requests func() []url.Values) {
	t.Helper()
	peers := compactPeersBlob(addrs)
	body := fmt.Sprintf("d8:intervali1800e5:peers%d:%se", len(peers), peers)

	var mu sync.Mutex
	var seen []url.Values

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.Query())
		mu.Unlock()
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	return srv.URL, func() []url.Values {
		mu.Lock()
		defer mu.Unlock()
		out := make([]url.Values, len(seen))
		copy(out, seen)
		return out
	}
}

// TestDownloadReportsTrackerEvents drives a real (local) download to completion and past it, then checks the
// tracker actually saw the three events the spec cares about, in order, with real (not placeholder) byte counts:
// started with left=full size, a completed announce once every piece is in with left=0, and stopped once the
// session shuts down.
func TestDownloadReportsTrackerEvents(t *testing.T) {
	tor, data := loadFixture(t)
	addrs := []netip.AddrPort{
		startSwarmSeeder(t, tor, data, swarmBehavior{}),
	}
	trackerURL, requests := recordingTrackerServer(t, addrs)
	tor.Announce = trackerURL
	tc := tracker.NewClient()
	outputPath := filepath.Join(t.TempDir(), tor.Name)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- Download(ctx, tor, tc, [20]byte{}, outputPath)
	}()

	// Wait for the file itself to land - Download keeps running as a seeder afterward (Step 10.6), so it never
	// returns on its own to signal completion.
	deadline := time.Now().Add(10 * time.Second)
	for {
		got, err := os.ReadFile(outputPath)
		if err == nil && bytes.Equal(got, data) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("download did not complete in time")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// The completed announce fires from the same goroutine that just wrote the last piece, a moment after the
	// file was already correct on disk - give it a beat to actually reach the tracker before cancelling.
	deadline = time.Now().Add(2 * time.Second)
	for {
		if reqs := requests(); len(reqs) > 0 && reqs[len(reqs)-1].Get("event") == "completed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("never saw a completed announce, got %d requests", len(requests()))
		}
		time.Sleep(20 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Download: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Download did not return after cancellation")
	}

	reqs := requests()
	if len(reqs) < 3 {
		t.Fatalf("got %d announces, want at least 3 (started, completed, stopped)", len(reqs))
	}

	first := reqs[0]
	if first.Get("event") != tracker.EventStarted {
		t.Errorf("first announce event = %q, want %q", first.Get("event"), tracker.EventStarted)
	}
	if want := fmt.Sprintf("%d", tor.TotalLength); first.Get("left") != want {
		t.Errorf("first announce left = %q, want %q (nothing downloaded yet)", first.Get("left"), want)
	}
	if first.Get("uploaded") != "0" || first.Get("downloaded") != "0" {
		t.Errorf("first announce uploaded/downloaded = %q/%q, want 0/0", first.Get("uploaded"), first.Get("downloaded"))
	}

	var sawCompleted bool
	for _, req := range reqs[1 : len(reqs)-1] {
		if req.Get("event") != tracker.EventCompleted {
			continue
		}
		sawCompleted = true
		if req.Get("left") != "0" {
			t.Errorf("completed announce left = %q, want 0", req.Get("left"))
		}
	}
	if !sawCompleted {
		t.Error("no announce carried event=completed")
	}

	if last := reqs[len(reqs)-1]; last.Get("event") != tracker.EventStopped {
		t.Errorf("last announce event = %q, want %q", last.Get("event"), tracker.EventStopped)
	}
}
