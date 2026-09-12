package download

import (
	"net/netip"
	"sync"
	"time"
)

// peerRate holds one peer's download and upload byte-count history, each its own rolling window - the same fixed-window, moving-average
// approach as Progress.recordSample/Rate, doubled for both directions.
type peerRate struct {
	downSamples []rateSample
	upSamples   []rateSample

	downTotal int64 // bytes ever received from this peer
	upTotal   int64 // bytes ever sent to this peer
}

// RateTracker records per-peer download/upload byte counts and reports each peer's current throughput - the entire input to the choking
// algorithm. It's written from every peer's own connection goroutine as bytes move, and read from the single choking goroutine that sorts
// peers by rate - unlike Progress's plain running counters, a rate needs its whole rolling window read and pruned together, which independent
// atomics can't do consistently, so this mutex-guarded instead of lock-free.
type RateTracker struct {
	mu    sync.Mutex
	peers map[netip.AddrPort]*peerRate
}

// NewRateTracker returns an empty tracker.
func NewRateTracker() *RateTracker {
	return &RateTracker{peers: make(map[netip.AddrPort]*peerRate)}
}

// AddDownloaded records n more bytes received from addr. Safe to call on a nil *RateTracker (a no-op), same as
// Progress, so callers that don't care about rate tracking can just pass nil.
func (rt *RateTracker) AddDownloaded(addr netip.AddrPort, n int) {
	if rt == nil {
		return
	}
	rt.record(addr, n, true)
}

// AddUploaded records n more bytes sent to addr. Safe to call on a nil *RateTracker.
func (rt *RateTracker) AddUploaded(addr netip.AddrPort, n int) {
	if rt == nil {
		return
	}
	rt.record(addr, n, false)
}

func (rt *RateTracker) record(addr netip.AddrPort, n int, download bool) {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	pr, ok := rt.peers[addr]
	if !ok {
		pr = &peerRate{}
		rt.peers[addr] = pr
	}

	now := time.Now()
	if download {
		pr.downTotal += int64(n)
		pr.downSamples = prune(append(pr.downSamples, rateSample{at: now, bytes: pr.downTotal}), now)
	} else {
		pr.upTotal += int64(n)
		pr.upSamples = prune(append(pr.upSamples, rateSample{at: now, bytes: pr.upTotal}), now)
	}
}

// prune drops samples older than rateSampleWindow - same pruning rule as Progress.recordSample.
func prune(samples []rateSample, now time.Time) []rateSample {
	cutoff := now.Add(-rateSampleWindow)
	i := 0
	for i < len(samples) && samples[i].at.Before(cutoff) {
		i++
	}
	return samples[i:]
}

// DownloadRate returns addr's current download rate in bytes/second, or 0 if unknown, stale, or rt is nil.
func (rt *RateTracker) DownloadRate(addr netip.AddrPort) float64 {
	if rt == nil {
		return 0
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()

	pr, ok := rt.peers[addr]
	if !ok {
		return 0
	}
	return rate(pr.downSamples)
}

// UploadRate returns addr's current upload rate in bytes/second, or 0 if unknown, stale, or rt is nil.
func (rt *RateTracker) UploadRate(addr netip.AddrPort) float64 {
	if rt == nil {
		return 0
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()

	pr, ok := rt.peers[addr]
	if !ok {
		return 0
	}
	return rate(pr.upSamples)
}

// rate computes bytes/second across samples the same way Progress.Rate does: first-to-last delta over elapsed time, treating a
// stale window (nothing recent) as zero instead of a stuck last-known value.
func rate(samples []rateSample) float64 {
	if len(samples) < 2 {
		return 0
	}
	first, last := samples[0], samples[len(samples)-1]
	if time.Since(last.at) > rateSampleWindow {
		return 0
	}
	elapsed := last.at.Sub(first.at).Seconds()
	if elapsed <= 0 {
		return 0
	}
	return float64(last.bytes-first.bytes) / elapsed
}

// Remove drops addr's tracked rates entirely - called when a peer disconnects, so a long-gone peer doesn't linger
// in the choking algorithm's peer list. Safe to call on a nil *RateTracker.
func (rt *RateTracker) Remove(addr netip.AddrPort) {
	if rt == nil {
		return
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	delete(rt.peers, addr)
}
