package tracker

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// announce-list's tiers are flattened into one ordered slice, ignoring the fallback announce URL.
func TestFlattenTiersWithAnnounceList(t *testing.T) {
	list := [][]string{
		{"http://a.example", "http://b.example"},
		{"http://c.example"},
	}
	got := flattenTiers("http://fallback.example", list)
	want := []string{"http://a.example", "http://b.example", "http://c.example"}

	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("index %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

// A UDP tracker (out of scope) is skipped so the next, working HTTP tracker still gets tried.
func TestAnnounceAllFallBackPastUDP(t *testing.T) {
	body := "d8:intervali1800e5:peers6:\x01\x02\x03\x04\x1a\xe1e"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(body))
	}))
	defer server.Close()

	announceList := [][]string{
		{"udp://dead.example:80"},
		{server.URL},
	}

	c := NewClient()
	resp, err := c.AnnounceAll("", announceList, func(trackerURL string) (string, error) {
		return trackerURL, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Peers) != 1 {
		t.Fatalf("expected 1 peer, got %d", len(resp.Peers))
	}
}

// If every tracker fails, the caller gets one combined error, not just the last one tried.
func TestAnnounceAllAllFail(t *testing.T) {
	c := NewClient()
	_, err := c.AnnounceAll("udp://dead.example:80", nil, func(trackerURL string) (string, error) {
		return trackerURL, nil
	})
	if err == nil {
		t.Fatal("expected an aggregated error when every tracker fails")
	}
}
