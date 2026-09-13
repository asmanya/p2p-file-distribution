package download

import "time"

// Every tunable constant this package uses lives here, one place, each with a comment explaining why that specific
// value was chosen - not just what it does. A few of these are `var`, not `const`, on purpose: they're timing
// values a test needs to shrink so it doesn't have to wait out a real 10- or 30-second cycle to exercise the
// behavior gated on it. Reassigning one in a test and restoring it via defer is the established pattern; see any
// test that does so for an example.

// --- Piece download ------------------------------------------------------

// backlogLimit is how many block requests stay pipelined in flight for a single piece at once, so round-trip
// latency overlaps across blocks instead of one request sitting idle while its answer is in transit. 5 is a
// starting point, not a measured optimum - see docs/architecture.md for the reasoning. Adaptive backlog sizing
// based on measured per-peer speed is a natural follow-up, not yet implemented.
const backlogLimit = 5

var (
	// pieceTimeout bounds how long a single piece download may take end to end, choke waits included, before the
	// coordinator gives up on the assignee and reassigns the piece elsewhere. Without this, a peer that goes
	// silent mid-piece - not disconnected, just never sending another byte - would hold that piece hostage forever.
	pieceTimeout = 30 * time.Second

	// readTimeout bounds a single read on a connection, reset on every message received. A peer that's merely slow
	// but still making progress survives; one that's gone silent past this gets dropped, well before pieceTimeout
	// would otherwise catch it.
	readTimeout = 15 * time.Second
)

// --- Piece selection and endgame -----------------------------------------

// assignmentTimeout is how long a single assignee stays in flight before the coordinator gives up on it and frees
// that assignee's slot for someone else. Without this, a half-open TCP connection - one where the peer is gone but
// the OS hasn't noticed yet - would hold a piece hostage forever, and the download would stall at 99% with no
// error and no log to explain why.
const assignmentTimeout = 30 * time.Second

// endgameThreshold is how many pieces may remain incomplete before endgame mode kicks in - low enough that assigning
// every remaining piece to every peer that has it only costs a small, bounded amount of duplicate bandwidth, high
// enough to actually eliminate the "last piece stuck on one slow peer" tail latency problem.
const endgameThreshold = 10

// --- Choking --------------------------------------------------------------

var (
	// chokeInterval is how often the tit-for-tat decision recalculates - the top unchokeSlots interested peers by
	// rate get unchoked, everyone else choked, 10 seconds matches the spec's convention.
	chokeInterval = 10 * time.Second

	// optimisticInterval is how often the optimistic-unchoke slot rotates to a new random peer, independent of the
	// regular recalc's own cycle.
	optimisticInterval = 30 * time.Second
)

// unchokeSlots is how many interested peers the regular (non-optimistic) algorithm keeps unchoked at once.
const unchokeSlots = 4

// --- Rate tracking ----------------------------------------------------------

// rateWindow bounds how far back Progress.Rate() looks when computing current download throughput for the display
// - a moving window, not a cumulative average, so the number reflects current speed, not a memory of however the
// download started. Shorter than rateSampleWindow below on purpose: this one drives what the user sees on screen,
// where responsiveness matters more than smoothing out a peer's normal jitter.
const rateWindow = 10 * time.Second

// rateSampleWindow bounds how far back a per-peer rate calculation looks for choking decisions - long enough that
// a second or two of jitter doesn't flip a choke decision, short enough that a peer's current behavior, not how
// the download started, drives it. This mirrors rateWindow above, just applied per peer instead of globally, and
// named separately since the two could reasonably diverge further later.
const rateSampleWindow = 20 * time.Second

// --- Coordinator and download loop ----------------------------------------

// coordinatorTickInterval is how often Run wakes up on its own, independent of incoming events, for stall
// detection and periodic bookkeeping.
const coordinatorTickInterval = 1 * time.Second

// progressLogInterval is how often the progress display redraws while a download or seed is running.
const progressLogInterval = 1 * time.Second

// stallTimeout is how long the download can go without a single piece completing before it's considered stalled
// and worth re-announcing to the tracker for a fresh peer list.
const stallTimeout = 30 * time.Second

// defaultAnnounceInterval is the routine re-announce cadence used until a tracker suggests its own via the
// announce response's "interval" field - the conventional default real trackers expect a client to fall back on.
const defaultAnnounceInterval = 30 * time.Minute

// defaultMaxPeers is the concurrent-connection cap used when Options.MaxPeers is left at its zero value, so callers
// that don't care about the limit (existing tests, for instance) don't have to think about it.
const defaultMaxPeers = 50

// resultsBufferSize is a small buffer so a worker sending a finished piece doesn't have to wait for the main
// goroutine to be ready to receive it right away.
const resultsBufferSize = 16

// shutdownGracePeriod is how long Download's main loop keeps servicing resultCh and the tracker after a caller
// asks it to stop, before it actually force-closes every connection - see the callerDone/shutdownDeadline
// handling in download.go.
var shutdownGracePeriod = 5 * time.Second
