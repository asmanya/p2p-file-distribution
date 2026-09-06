package download

import (
	"sync/atomic"
	"time"
)

// rateWindow bounds how far back Rate() looks when computing current throughput - a moving window, not a cumulative
// average, so the number reflects current speed, not a memory of however the download started.
const rateWindow = 10 * time.Second

// Progress tracks live download progress. bytesDownloaded and activePeers are updated from many worker goroutines
// concurrently, so they're atomic; everything else (piecesDone, the rate samples) is touched only by the main
// download goroutine
type Progress struct {
	PieceCount int

	bytesDownloaded int64 // atomic
	activePeers     int64 // atomic
	peakPeers       int64 // atomic - highest activePeers has ever reached

	connectAttempts  int64 // atomic
	connectSuccesses int64 // atomic

	hashFailures int64 // atomic - pieces that downloaded fully but failed SHA-1 verification
	panics       int64 // atomic - worker goroutines that recovered from a panic

	piecesDone int
	samples    []rateSample
}

type rateSample struct {
	at    time.Time
	bytes int64
}

func NewProgress(pieceCount int) *Progress {
	return &Progress{PieceCount: pieceCount}
}

// AddBytes records n more downloaded bytes. Safe to call from any goroutine, and safe to call on a nil *Progress
// (a no-op) so callers that don't care about progress reporting can just pass nil.
func (p *Progress) AddBytes(n int) {
	if p == nil {
		return
	}
	atomic.AddInt64(&p.bytesDownloaded, int64(n))
}

// BytesDownloaded returns the current total. Safe to call concurrently.
func (p *Progress) BytesDownloaded() int64 {
	if p == nil {
		return 0
	}
	return atomic.LoadInt64(&p.bytesDownloaded)
}

// PeerConnected/PeerDisconnected track how many workers currently have a live connection. Called from worker
// goroutine as they start and exit, hence atomic rather than a plain counter.
func (p *Progress) PeerConnected() {
	if p == nil {
		return
	}
	n := atomic.AddInt64(&p.activePeers, 1)
	// Racing CompareAndSwap loop instead of a mutex: activePeers can dip and
	// climb again as peers connect and disconnect, but peakPeers only ever
	// needs to remember the highest value it has ever seen.
	for {
		peak := atomic.LoadInt64(&p.peakPeers)
		if n <= peak || atomic.CompareAndSwapInt64(&p.peakPeers, peak, n) {
			break
		}
	}
}

func (p *Progress) PeerDisconnected() {
	if p != nil {
		atomic.AddInt64(&p.activePeers, -1)
	}
}

func (p *Progress) ActivePeers() int64 {
	if p == nil {
		return 0
	}
	return atomic.LoadInt64(&p.activePeers)
}

// PeakPeers returns the highest number of simultaneously connected peers this download has ever had.
func (p *Progress) PeakPeers() int64 {
	if p == nil {
		return 0
	}
	return atomic.LoadInt64(&p.peakPeers)
}

// ConnectAttempted/ConnectSucceeded track how many peer dial attempts were made and how many completed a handshake,
// so the caller can report a connection success rate - tracker-returned peers are commonly 60-80% dead or unreachable,
// and that's a normal, worth-recording fact about the swarm, not a bug.
func (p *Progress) ConnectAttempted() {
	if p != nil {
		atomic.AddInt64(&p.connectAttempts, 1)
	}
}

func (p *Progress) ConnectSucceeded() {
	if p != nil {
		atomic.AddInt64(&p.connectSuccesses, 1)
	}
}

// ConnectStats returns the raw attempt/success counts behind the connection success rate.
func (p *Progress) ConnectStats() (attempts, successes int64) {
	if p == nil {
		return 0, 0
	}
	return atomic.LoadInt64(&p.connectAttempts), atomic.LoadInt64(&p.connectSuccesses)
}

// HashFailed records a piece that downloaded completely but failed SHA-1 verification - a peer sent bad data.
func (p *Progress) HashFailed() {
	if p != nil {
		atomic.AddInt64(&p.hashFailures, 1)
	}
}

func (p *Progress) HashFailures() int64 {
	if p == nil {
		return 0
	}
	return atomic.LoadInt64(&p.hashFailures)
}

// PanicRecovered records that a worker goroutine panicked and was recovered, so the panic isn't only visible as a
// one-off log line during development - a nonzero count here after a long run is worth investigating.
func (p *Progress) PanicRecovered() {
	if p != nil {
		atomic.AddInt64(&p.panics, 1)
	}
}

func (p *Progress) Panics() int64 {
	if p == nil {
		return 0
	}
	return atomic.LoadInt64(&p.panics)
}

// PieceCompleted records that one more piece finished and takes a rate sample. Must only be called from the
// main download goroutine.
func (p *Progress) PieceCompleted() {
	if p == nil {
		return
	}
	p.piecesDone++
	p.recordSample()
}

func (p *Progress) recordSample() {
	now := time.Now()
	p.samples = append(p.samples, rateSample{at: now, bytes: p.BytesDownloaded()})

	// moving window implementation - removing old samples
	cutoff := now.Add(-rateWindow)
	i := 0
	for i < len(p.samples) && p.samples[i].at.Before(cutoff) {
		i++
	}
	p.samples = p.samples[i:]
}

// Rate returns the current download rate in bytes/second, computed over the last rateWindow - not since the
// download started, which would keep remembering a slow start forever.
func (p *Progress) Rate() float64 {
	if p == nil || len(p.samples) < 2 {
		return 0
	}
	first, last := p.samples[0], p.samples[len(p.samples)-1]
	elapsed := last.at.Sub(first.at).Seconds()
	if elapsed <= 0 {
		return 0
	}
	return float64(last.bytes-first.bytes) / elapsed
}

// Percent returns completion percentage, 0-100. Must only be called from the main download goroutine (piecesDone is
// not atomic).
func (p *Progress) Percent() float64 {
	if p == nil || p.PieceCount == 0 {
		return 0
	}
	return 100 * float64(p.piecesDone) / float64(p.PieceCount)
}

// ETA estimates remaining time based on the current rate. Returns 0 when the rate is unknown (e.g. right at start)
func (p *Progress) ETA(totalBytes int64) time.Duration {
	rate := p.Rate()
	if rate <= 0 {
		return 0
	}
	remaining := float64(totalBytes) - float64(p.BytesDownloaded())
	if remaining < 0 {
		remaining = 0
	}
	return time.Duration(remaining/rate) * time.Second
}
