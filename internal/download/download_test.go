package download

import (
	"context"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"runtime"
	"testing"
	"time"

	"github.com/asmanya/p2p-file-distribution/internal/tracker"
)

// compactPeersBlob packs addrs into the compact peer format a tracker
// response uses: 4 bytes IPv4 + 2 bytes port, back to back.
func compactPeersBlob(addrs []netip.AddrPort) []byte {
	buf := make([]byte, 0, 6*len(addrs))
	for _, a := range addrs {
		ip4 := a.Addr().As4()
		buf = append(buf, ip4[:]...)
		var portBytes [2]byte
		binary.BigEndian.PutUint16(portBytes[:], a.Port())
		buf = append(buf, portBytes[:]...)
	}
	return buf
}

// fakeTrackerServer starts an httptest server that always answers any
// announce with a fixed compact peer list, and returns its URL. The server
// is closed automatically when the test ends.
func fakeTrackerServer(t *testing.T, addrs []netip.AddrPort) string {
	t.Helper()
	peers := compactPeersBlob(addrs)
	body := fmt.Sprintf("d8:intervali1800e5:peers%d:%se", len(peers), peers)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// someUnreachableAddrs returns n loopback addresses nothing is listening
// on - dialing them fails fast with "connection refused" rather than
// hanging until a dial timeout, which is what makes this useful for tests
// that only care about clean shutdown, not a real download.
func someUnreachableAddrs(n int) []netip.AddrPort {
	addrs := make([]netip.AddrPort, n)
	for i := 0; i < n; i++ {
		addrs[i] = netip.MustParseAddrPort(fmt.Sprintf("127.0.0.1:%d", 40000+i))
	}
	return addrs
}

// TestDownloadCleansUpOnCancel confirms that cancelling a download leaves
// no goroutines behind - every worker, its cancellation watcher, and any
// other background goroutine must exit once ctx is cancelled.
func TestDownloadCleansUpOnCancel(t *testing.T) {
	tor, _ := loadFixture(t)
	tor.Announce = fakeTrackerServer(t, someUnreachableAddrs(5))
	tc := tracker.NewClient()

	before := runtime.NumGoroutine()

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)
		Download(ctx, tor, tc, [20]byte{})
	}()

	time.Sleep(50 * time.Millisecond) // let Download start and dial attempts begin
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Download did not return after cancellation")
	}

	time.Sleep(100 * time.Millisecond) // give unwinding goroutines a beat
	after := runtime.NumGoroutine()

	if after > before {
		t.Errorf("goroutine leak: had %d before, %d after cancellation", before, after)
	}
}
