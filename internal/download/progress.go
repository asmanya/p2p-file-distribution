package download

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Progress tracks live download progress. bytesDownloaded and activePeers are updated from many worker goroutines
// concurrently, so they're atomic; everything else (piecesDone, the rate samples) is touched only by the main
// download goroutine
type Progress struct {
	PieceCount int

	bytesDownloaded int64 // atomic
	bytesUploaded   int64 // atomic
	activePeers     int64 // atomic
	peakPeers       int64 // atomic - highest activePeers has ever reached

	// uploadSamples backs UploadRate the same way samples backs Rate, but AddUploadedBytes is called from every
	// peer's own connection goroutine while seeding - genuinely concurrent, unlike piecesDone/samples below which
	// only the main download goroutine ever touches - so this one needs its own mutex.
	uploadMu      sync.Mutex
	uploadSamples []rateSample

	connectAttempts  int64 // atomic
	connectSuccesses int64 // atomic

	hashFailures int64 // atomic - pieces that downloaded fully but failed SHA-1 verification
	panics       int64 // atomic - worker goroutines that recovered from a panic

	duplicateAssignments int64 // atomic - endgame assignments cancelled because another peer finished first

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

// AddUploadedBytes records n more bytes sent to peers we're seeding to - the counterpart to AddBytes for the upload direction.
// Safe to call from any goroutine, and safe on a nil *Progress.
func (p *Progress) AddUploadedBytes(n int) {
	if p == nil {
		return
	}
	total := atomic.AddInt64(&p.bytesUploaded, int64(n))

	p.uploadMu.Lock()
	p.uploadSamples = recordAndPrune(p.uploadSamples, total)
	p.uploadMu.Unlock()
}

// UploadRate returns the current upload rate in bytes/second, computed over the last rateWindow. Safe to call
// concurrently.
func (p *Progress) UploadRate() float64 {
	if p == nil {
		return 0
	}
	p.uploadMu.Lock()
	defer p.uploadMu.Unlock()
	return rateFromSamples(p.uploadSamples)
}

// BytesUploaded returns the current total. Safe to call concurrently.
func (p *Progress) BytesUploaded() int64 {
	if p == nil {
		return 0
	}
	return atomic.LoadInt64(&p.bytesUploaded)
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

// DuplicateAssignment records one endgame assignment that got cancelled because another peer finished the same
// piece first - a measured proxy for bandwidth spent on redundant requests, not an estimate.
func (p *Progress) DuplicateAssignment() {
	if p != nil {
		atomic.AddInt64(&p.duplicateAssignments, 1)
	}
}

func (p *Progress) DuplicateAssignments() int64 {
	if p == nil {
		return 0
	}
	return atomic.LoadInt64(&p.duplicateAssignments)
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
	p.samples = recordAndPrune(p.samples, p.BytesDownloaded())
}

// Rate returns the current download rate in bytes/second, computed over the last rateWindow - not since the
// download started, which would keep remembering a slow start forever.
func (p *Progress) Rate() float64 {
	if p == nil {
		return 0
	}
	return rateFromSamples(p.samples)
}

// recordAndPrune appends one sample and drops everything older than rateWindow - the moving-window implementation
// shared by Progress's download-rate and upload-rate tracking.
func recordAndPrune(samples []rateSample, bytes int64) []rateSample {
	now := time.Now()
	samples = append(samples, rateSample{at: now, bytes: bytes})

	cutoff := now.Add(-rateWindow)
	i := 0
	for i < len(samples) && samples[i].at.Before(cutoff) {
		i++
	}
	return samples[i:]
}

// rateFromSamples computes bytes/second across a sample window. Returns 0 if there aren't at least two samples, or
// if the newest one is stale (a real stall, not just a slow window) - without that check, a rate would keep
// reporting whatever it last computed instead of admitting nothing recent has happened.
func rateFromSamples(samples []rateSample) float64 {
	if len(samples) < 2 {
		return 0
	}
	first, last := samples[0], samples[len(samples)-1]
	if time.Since(last.at) > rateWindow {
		return 0
	}
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

// barWidth is the fixed character width of the progress bar itself, not counting the surrounding brackets or the
// status text - wide enough to read at a glance, narrow enough to fit one terminal line alongside that text.
const barWidth = 40

// ANSI escape codes for the progress display - raw sequences, no library needed. Windows Terminal and modern
// PowerShell/conhost interpret these by default; a plain-text viewer (output piped to a file, say) just sees a
// few extra bytes around otherwise-readable text, so this degrades safely instead of breaking anything.
const (
	ansiReset   = "\x1b[0m"
	ansiBold    = "\x1b[1m"
	ansiGreen   = "\x1b[32m"
	ansiCyan    = "\x1b[36m"
	ansiYellow  = "\x1b[33m"
	ansiMagenta = "\x1b[35m"
	ansiGray    = "\x1b[90m"
)

// Render returns a multi-line, colorized status block: the bar (or a "SEEDING" header) on its own line, followed
// by a small aligned table of the underlying numbers. Every line starts with an ANSI "clear line" code so
// re-printing this block in place (see the redraw closure in download.go) never leaves stale characters behind
// when a line gets shorter than it was on the previous tick.
func (p *Progress) Render(totalBytes int64, seeding bool) []string {
	const clear = "\x1b[2K"

	// Blank rows (top, between the header and the table, and bottom) visually separate this block from the plain
	// slog lines around it (the resume/completion messages) - every row is redrawn every tick regardless of
	// content, so these need to be part of the slice like any other line, not printed once and forgotten.
	if seeding {
		uploaded := p.BytesUploaded()
		var ratio float64
		if totalBytes > 0 {
			ratio = float64(uploaded) / float64(totalBytes)
		}
		return []string{
			"",
			clear + fmt.Sprintf("%s%sSEEDING%s", ansiBold, ansiGreen, ansiReset),
			"",
			clear + fmt.Sprintf("  %-10s %s%d%s", "Peers", ansiMagenta, p.ActivePeers(), ansiReset),
			clear + fmt.Sprintf("  %-10s %s%.1f KiB/s%s", "Upload", ansiGreen, p.UploadRate()/1024, ansiReset),
			clear + fmt.Sprintf("  %-10s %.1f MiB", "Uploaded", float64(uploaded)/(1024*1024)),
			clear + fmt.Sprintf("  %-10s %s%.2f%s", "Ratio", ansiYellow, ratio, ansiReset),
			"",
		}
	}

	filled := int(p.Percent() / 100 * barWidth)
	if filled > barWidth {
		filled = barWidth
	}
	bar := ansiGreen + strings.Repeat("=", filled) + ansiReset +
		ansiGray + strings.Repeat(" ", barWidth-filled) + ansiReset

	return []string{
		"",
		clear + fmt.Sprintf("[%s] %s%5.1f%%%s", bar, ansiBold, p.Percent(), ansiReset),
		"",
		clear + fmt.Sprintf("  %-10s %d/%d", "Pieces", p.piecesDone, p.PieceCount),
		clear + fmt.Sprintf("  %-10s %s%.1f KiB/s%s", "Speed", ansiCyan, p.Rate()/1024, ansiReset),
		clear + fmt.Sprintf("  %-10s %s%d%s", "Peers", ansiMagenta, p.ActivePeers(), ansiReset),
		clear + fmt.Sprintf("  %-10s %s%s%s", "ETA", ansiYellow, p.ETA(totalBytes).Round(time.Second), ansiReset),
		"",
	}
}
