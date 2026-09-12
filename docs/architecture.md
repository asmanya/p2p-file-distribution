# Architecture

The long version. The [README](../README.md) covers what this client
does and the decisions worth knowing about in a couple of minutes; this
document is for reading the code afterwards, and covers why each
package is shaped the way it is, who owns which piece of mutable state,
and which bugs shaped the current design.

Measured numbers, micro-benchmarks, and profiles live separately in
[performance.md](performance.md).

**Contents**

- [Layers](#layers) - the seven packages and the one-directional dependency rule
- [Data flow](#data-flow) - `.torrent` file to bytes on disk, step by step
- [State ownership](#state-ownership) - every piece of mutable state and who owns it
- [Error handling philosophy](#error-handling-philosophy) - what returns an error, what panics, what's fatal
- [Trust boundaries and guards](#trust-boundaries-and-guards) - every untrusted input and the check it gets
- [Design notes](#design-notes) - package-by-package reasoning

## Layers

Seven layers. A package can depend on anything listed below it, never on
anything above.

```
7 cmd/p2pget          CLI. Flags + wiring only. No logic.
6 internal/download   Orchestration. Only package with full system visibility.
5 internal/storage    Disk I/O, resume verification.
4 internal/tracker    HTTP announce. Does not talk to peers.
3 internal/peer       Wire protocol + connection lifecycle.
2 internal/piece      Geometry + SHA-1 verification. Pure, no I/O.
2 internal/metainfo   .torrent parsing + info hash. No network, no disk writes.
1 internal/bencode    Serialization. Zero knowledge of torrents.
```

`bencode` never references torrents, pieces, or peers. `metainfo` never
opens a socket or writes to disk. `peer` implements **mechanism** -
sending and parsing messages - never **policy**; deciding which piece
to request next belongs to `download`. `piece` does no I/O at all:
every function takes bytes and integers in, returns a value. `cmd` has
no logic beyond flag parsing and a single call into `internal/download`.

## Data flow

`.torrent` file to bytes on disk, in order:

1. **Parse** (`metainfo.ParseFile`) - bencode-decodes the file, builds
   a typed `Torrent`, computes the info hash two independent ways and
   cross-checks them.
2. **Announce** (`tracker.Client.AnnounceAll`) - builds the announce
   URL from the info hash, peer ID, and port; tries every tracker in
   `announce-list` in order until one responds; decodes the peer list
   (compact or legacy format).
3. **Resume check** (`storage.VerifyExisting`) - re-hashes whatever's
   already on disk at the output path against the torrent's expected
   piece hashes; anything that matches is marked complete before a
   single connection opens.
4. **Connect** (`peer.Dial` -> `peer.Handshake`) - one worker goroutine
   per discovered address; a successful handshake hands the connection
   to `download.runConnection`.
5. **Select** (`Coordinator.selectPieceFor`) - rarest-first among the
   pieces that peer's bitfield has and this client doesn't; assigned
   over that peer's own `commands` channel.
6. **Download** (`download.Piece` / `downloadBlocks`) - pipelined
   block requests (`peer.SendRequest`), up to `backlogLimit` in flight
   at once, assembled into one buffer as `piece` messages arrive.
7. **Verify** (`piece.Verify`) - SHA-1 over the assembled buffer,
   checked against the torrent's expected hash for that index.
8. **Write** (`storage.File.WriteAt`) - a verified piece is written to
   its exact byte offset in the preallocated output file; unverified
   data is never written.
9. **Report** (`Progress`, tracker re-announce) - byte counters update
   atomically as data moves; the tracker hears `started`/`completed`/
   `stopped` at the right moments, and routine re-announces in between.

Once every piece is in, steps 5 onward run in reverse for any peer
that requests a piece this client now has - the same connection, the
same code path, serving instead of requesting.

## State ownership

| State | Owner | Notes |
|-------|-------|-------|
| `Coordinator.pieces`, `.availability`, `.peers`, `.assignments`, `.endgame` | `Coordinator.Run`'s single goroutine, reached only through its `events` channel | No mutex anywhere in the coordinator - nothing outside that one goroutine ever touches this state |
| `resultCh` (verified results) | No owner, but every send from the coordinator races `ctx.Done()` in a `select` | The buffer is small and fixed, not sized to piece count, so an unconditional send could hang forever with no reader during shutdown |
| `Progress.bytesDownloaded`, `.activePeers`, `.peakPeers`, `.connectAttempts/Successes`, `.hashFailures`, `.panics`, `.duplicateAssignments` | Any worker goroutine, or the coordinator's own goroutine | Plain `sync/atomic` counters, no mutex |
| `Progress.piecesDone`, `.samples` (rate window) | `Download`'s main goroutine only | Neither a worker nor the coordinator ever touches these |
| `connected` (dialed peer addresses, inside `Download`) | `Download`'s main goroutine, through the `announce` closure | The mutex is defensive rather than load-bearing - `announce` is only ever called from the main goroutine |
| `HaveBitfield.bf` (pieces already verified or on disk) | Mutex-guarded | `Download`'s main goroutine writes as pieces complete; every connection goroutine now reads it concurrently (`Has` when serving a request, `Snapshot` when sending our own bitfield) - the concurrent-reader case the mutex was already sized for |
| `storage.File` writes (`WriteAt` per piece) | No owner needed | Each piece owns a disjoint byte range, and positional writes to non-overlapping ranges don't need a lock |
| `RateTracker.peers` (per-peer rolling download/upload rates) | Mutex-guarded | Written from every connection goroutine as bytes move, read from the coordinator's own goroutine on every choke recalc - genuinely concurrent on both sides, unlike the coordinator's other state |
| `Coordinator.peerInfo.{interested, choked, peerChoking, gotBitfield}`, `.optimisticAddr`, `.hasOptimistic` | `Coordinator.Run`'s single goroutine | Same invariant as the rest of `Coordinator`'s state - reached only through events, no mutex needed |
| `Listener.total`, `.perIP` (accepted-connection counters) | Mutex-guarded | `admit`/`release` run from the accept loop and from every connection's own exit path concurrently - a plain counter pair, the case the concurrency rule allows a mutex for |
| `Listener.handlers` (accepted-connection `WaitGroup`) | The `Listener` itself; `Add` happens inside `Serve`, before the handler goroutine starts | Keeps the `Add` and the caller's `Wait` from racing, which calling `Add` from inside the handler goroutine itself would risk |

## Error handling philosophy

- **Errors, not panics, for anything untrusted.** Malformed bencode, a
  corrupt `.torrent`, a hostile peer message - every one of these
  returns an `error` up the call stack. A panic is reserved for this
  client's own programming mistakes, never for another party's input.
- **Wrapped, not swallowed.** Every error that crosses a package
  boundary is wrapped with `fmt.Errorf("%w", ...)` and enough context
  to say where it happened, so a failure three layers down still
  reads clearly at the top.
- **Distinguishable, not generic.** Connection-refused, handshake
  timeout, protocol mismatch, and info-hash mismatch are different,
  named outcomes on purpose - collapsing them into one "connection
  failed" would make a dead swarm indistinguishable from a real bug.
- **One goroutine's panic doesn't take the download down.** Every
  worker recovers from a panic, logs the full stack trace, and counts
  it, so one malformed message from one bad peer can't crash every
  other in-flight connection.
- **A failed peer is not a failed download.** Most tracker-returned
  addresses are dead or unreachable; most swarms have at least one
  peer that sends bad data. Both are the expected, normal case
  (retry, reassign, re-request), not exceptional errors that abort
  anything.
- **Fatal is reserved for what actually stops the download.** A disk
  that can't be written, a torrent file that won't parse, every
  tracker failing at once - these return an error from `Download`
  itself; an individual peer or piece failure never does.

## Trust boundaries and guards

Everything crossing into this program from outside is treated as
hostile until it proves otherwise. The guards, by where the untrusted
data enters:

| Boundary | Guard |
|---|---|
| `.torrent` file (bencode) | String length and dictionary nesting depth capped before allocating; malformed input returns an error, never a panic |
| `.torrent` file (metainfo) | `name` rejected if empty, absolute, or containing a path separator or leading dot (path traversal); piece count cross-checked against `TotalLength`/`PieceLength` |
| Tracker response | Request timeout (Go's default HTTP client has none); response body size capped before it's read; non-200 status rejected before the body is parsed as bencode |
| Peer handshake | Info hash and protocol string checked before this client's own identity is ever sent back; dial and handshake each carry their own deadline |
| Peer messages | 4-byte length prefix capped before a buffer is allocated for it; every typed payload (`have`, `request`, `piece`, `cancel`) validated against this torrent's actual piece count and piece length before use |
| Incoming block requests | Bounds-checked against the requested piece's real length, not the torrent's standard length; capped at the standard block size regardless of what's asked for |
| Disk on resume | Existing data is re-hashed, never assumed correct from a side-channel record; a missing, wrong-size, or corrupt file is treated identically - everything not proven gets re-downloaded |

The one deliberate exception: pieces this client has already
downloaded and verified this session are trusted without re-hashing
when served to another peer. They were checked once, at the only
point corruption could plausibly enter - over the network - and
re-checking on every serve would cost real CPU for no added safety.

## Design notes

Package-by-package reasoning, roughly bottom-up:

- **[`internal/bencode`](#internalbencode)** - sealed `Value` interface, guard-before-allocate, canonical encoding.
- **[`internal/metainfo`](#internalmetainfo)** - bencode tree to typed `Torrent`, path-traversal defense, dual info-hash computation.
- **[`internal/tracker`](#internaltracker)** - HTTP announce with three network guards, compact/legacy peer formats, multi-tracker fallback.
- **[`internal/peer`](#internalpeer)** - wire protocol, dial and accept, deadlines, fault-injected connection tests.
- **[`internal/piece`](#internalpiece)** - pure geometry math and SHA-1 verification, no I/O.
- **[`internal/download`](#internaldownload)** - the coordinator, both concurrency designs, choking, seeding, resume.
- **[`internal/storage`](#internalstorage)** - preallocation, positional writes, hash-based resume.

### internal/bencode

The lowest layer. Bencode serialization with no idea torrents, pieces,
or peers exist. Almost everything else in the client rests on this
being both exhaustively type-safe and safe against hostile input, since
`.torrent` files and tracker responses are the first untrusted data it
touches.

Four concrete types - `ByteString`, `Integer`, `List`, `Dictionary` -
satisfy a sealed `Value` interface through an unexported marker method,
so nothing outside the package can add a fifth. Every type switch on
`Value` elsewhere in the codebase stays exhaustive without a `default`
case quietly swallowing a missed type. That was a deliberate choice over
a reflection-based decoder, which would push the same class of mistake
to runtime instead of catching it at compile time.

Size and depth guards run before allocation, not after - a string
length or nesting depth gets checked against a fixed cap before any
buffer exists or any recursive call happens, so an oversized length
prefix or a deeply nested input costs nothing to reject. Byte strings
stay opaque bytes rather than text; the decoder never validates or
normalizes them as UTF-8, because piece hashes are raw binary and any
implicit text handling would silently corrupt them.

Dictionary keys are required to be strictly ascending on decode, and
sorted byte-wise (never locale-aware) on encode. Both halves matter:
encoding has to produce the same canonical byte sequence every time for
the info-hash computation to be reproducible, and decoding has to reject
anything that doesn't already satisfy that form, so a malformed file
fails right where the problem is instead of surfacing later as a
mysterious hash mismatch.

The real proof of correctness isn't the unit tests, it's the round-trip
and fuzz tests. Decoding and re-encoding both real `.torrent` fixtures
byte-for-byte is what demonstrates the encoder is canonical and the
decoder lossless - exactly what the info-hash computation depends on. A
native Go fuzz target, seeded from every known edge case, checks that
same decode/encode pair under random and mutated input, and confirms
malformed input always comes back as an error, never a panic.

### internal/metainfo

Turns a parsed bencode tree into the flat, typed `Torrent` struct the
rest of the client works with, and computes the 20-byte info hash that
identifies a torrent to trackers and peers. No network access, no disk
writes beyond reading the `.torrent` file itself.

The bencode tree and the `Torrent` struct are deliberately different
shapes - one generic and nested, the other flat and specific to what a
torrent actually needs - and `Parse` is the one explicit boundary
between them, so the tree's general-purpose shape never leaks further
into the client. `InfoHash` is a fixed-size `[20]byte` rather than a
slice: comparable with `==`, usable as a map key, its size guaranteed at
compile time. Piece hashes (`[][20]byte`) follow the same reasoning,
since they get compared against freshly-computed hashes constantly once
downloading starts.

Files are already modeled as a list, even though only single-file
torrents parse right now. A single-file torrent is just a list with one
`FileEntry` (`Path` set to the name, `Length` to the total length), so
adding real multi-file support later means extending that list rather
than reworking everything - storage offsets included - that assumed
exactly one file.

A torrent's `name` field is untrusted input and gets validated before
anything else touches it: rejected if it's empty, contains a path
separator, starts with a dot (catching `.` and `..`), or is an absolute
path. That check exists because the name eventually becomes an output
file path, and without it a crafted `.torrent` could use a name like
`../../.ssh/authorized_keys` to write outside the intended download
directory.

Piece count is derived and cross-checked rather than trusted as given -
`ceil(TotalLength / PieceLength)` is compared against the actual number
of piece hashes present, and a mismatch means the file is corrupt or
malicious and gets rejected during parsing instead of discovered
mid-download.

The info hash is computed two independent ways. Method A re-encodes the
parsed `info` dictionary through the bencode encoder and hashes that.
Method B hashes the dictionary's original bytes directly, located
through byte offsets the decoder records while parsing - a narrow,
justified amendment to `bencode`, which just reports offsets generically
and has no idea what a caller does with them. `Parse` checks that both
agree before returning. Right now that check can only ever pass, since
the decoder already rejects non-canonically-ordered dictionaries, so
Method A and Method B are structurally guaranteed to match. Its value is
future-facing: it's what would let strict key ordering be relaxed later,
to accept real-world `.torrent` files that don't quite follow spec,
without ever risking a silently wrong info hash.

A golden test pins every parsed field against ground truth recorded
independently, via `transmission-show` rather than this project's own
code, for both fixture torrents. It's the one test in the suite that can
catch a bug this project's own reasoning would reproduce and miss, and
it should never be deleted.

### internal/tracker

Turns a torrent's announce information into a live list of peer
addresses. Its only job is figuring out who to talk to - it never opens
a connection to a peer itself - and it treats every response as hostile
until proven otherwise, the same posture `bencode` and `metainfo` take
toward their own input.

Peer addresses are `net/netip.AddrPort` rather than a custom struct:
comparable and usable as a map key at zero allocation cost, and it
represents IPv4 and IPv6 through the same type, both of which matter
once duplicate peers need deduplicating. The peer ID is generated once
per session with `crypto/rand`, following the Azureus-style convention
(`-GO0001-` followed by 12 random bytes) - it's how a tracker tells this
client's connections apart from everyone else in the swarm, and while
predictability here doesn't buy an attacker much, crypto-grade
randomness costs nothing to use anyway.

The info hash and peer ID go into the announce URL as raw bytes,
percent-encoded, never as hex. Go's `net/url` query encoding handles
arbitrary byte strings correctly on its own. Sending hex is the common
mistake here - it produces a URL that looks plausible and fails against
every real tracker, since the tracker ends up seeing the wrong 20 bytes
entirely.

The HTTP client carries three guards that all come from the same
principle: treat anything the network hands you as hostile until it's
proven otherwise. An explicit timeout, since Go's default client has
none and a dead tracker would otherwise hang the program forever. A hard
cap on how much of the response body gets read, so an oversized or
slow-drip body can't exhaust memory. And a status-code check before the
body is ever parsed, because a non-200 response is usually an HTML error
page, not bencode.

Both compact and legacy peer list formats are supported, chosen by
inspecting the actual bencode type of the `peers` value rather than
trusting that a tracker honored `compact=1`. Real-world trackers
sometimes ignore that request, and the parser has to survive it rather
than fail on a torrent that would otherwise work fine. Along the same
lines, a failed announce to one tracker doesn't fail the whole request -
`announce-list` (BEP-12) gets flattened into an ordered list of URLs and
tried in order, and a URL with an unsupported scheme (UDP trackers are
out of scope here) or one that errors is skipped rather than fatal. This
matters in practice, since most real torrents list a UDP tracker first
with an HTTP fallback further down; skipping instead of aborting is what
makes real torrents work at all.

Integration tests run against a local `httptest` server rather than a
real tracker. Real trackers are flaky, rate-limit repeated hits, and
return a different peer list every time, none of which is reproducible
in CI. A local server can deterministically produce failure modes -
garbage body, non-200 status, a slow response, an oversized body - that
would be impractical to trigger against the real thing on demand. The
client was separately verified once against a real public tracker
(Debian's), which returned a genuine list of peers for a real torrent;
that one-time run is the actual proof this layer works outside of
tests.

### internal/peer

Implements the BitTorrent wire protocol: pure encoding and decoding of
handshakes and messages, plus a thin connection layer to exchange them
over TCP. It carries no download policy - deciding which piece to
request next belongs to `download`, not here.

Serialization and parsing are pure functions, kept completely separate
from the network code that uses them. `Handshake.Serialize` and
`ParseHandshake` take and return bytes; neither touches a socket. A
wire-format bug shows up as a plain byte-comparison test failure instead
of a flaky-looking network test, the same separation `bencode`'s
encoder/decoder and `metainfo`'s parser already lean on, applied here
for the first time to something a socket carries.

The connection logic splits into a public `Dial`, which owns the TCP
connection, and an internal `handshakeOver`, which owns the handshake
exchange over any `net.Conn`. Production code never sees that split; it
exists so tests can hand `handshakeOver` one end of an in-memory
`net.Pipe` instead of a real socket and simulate failure modes - a
mismatched info hash, a wrong protocol string, a peer that disconnects
mid-handshake, one that never responds at all - that would be
impractical to trigger against a real connection on demand.

One deadline covers the dial, and a separate one covers the full
handshake exchange, both the write and the read. A deadline set only
before the read would still let a peer that accepts a connection and
never reads hang the write, since a full send buffer blocks `Write` too.
That handshake deadline gets explicitly cleared the moment the exchange
succeeds - leaving it in place is the kind of bug that doesn't show up
until much later, when the same stale deadline fires in the middle of
unrelated work and looks like an unrelated, intermittent failure.

Connection-refused, handshake timeout, protocol mismatch, and info-hash
mismatch are distinguishable outcomes, not one generic error. Most
addresses a tracker hands back belong to peers that are offline,
unreachable, or gone, which is normal, not a bug - and the only way to
tell "the swarm is mostly dead right now" from "my handshake code is
broken" is if the failure reasons stay visible instead of collapsing
into one message.

`handshakeTimeout` is a package variable rather than a constant purely
so tests can shrink it. A test proving a slow or silent peer triggers a
timeout still has to wait for that timeout to fire, and letting the test
override the duration is the difference between it taking 50
milliseconds and taking 5 seconds for the same coverage. A related trap:
`net.Pipe`'s two ends aren't independent the way two ends of a real TCP
connection are. Closing one side makes deadline and I/O calls on the
other side start failing too, which real sockets don't do. A test that
closes its fake peer's end right after writing a response can race the
code under test if that code is still finishing up (clearing its own
deadline, say) on the other end - the fix is for the test to simply not
close early, not to add synchronization the production code doesn't
need.

Dialing every address a real tracker returned for a real torrent
completed handshakes with a mix of real BitTorrent clients (qBittorrent,
Transmission, Deluge, libtorrent) at roughly the success rate the design
expects, with every failure falling cleanly into one of the categories
above rather than an unexplained one.

Keep-alive gets a sentinel message ID rather than being represented as
`nil`. Returning `nil, nil` from a read function is a classic Go
footgun - every caller has to remember to check for it, and once
connections run concurrently, the one caller that forgets is a
nil-pointer panic waiting to happen. A sentinel keeps keep-alive as just
another case in an exhaustive switch, which the type system helps
enforce and a missed check can't silently compile away.

Message framing guards the length prefix before allocating a buffer for
it, the same guard-before-allocate discipline `bencode` established for
string lengths and nesting depth - a 4-byte prefix claiming gigabytes
gets rejected on the spot instead of turning into an allocation first.
Every typed payload parser (`have`, `request`, `piece`) validates a
peer's numbers against this torrent's actual piece count and piece
length before trusting them, since a peer's request is input, not fact;
an out-of-range piece index or a block that overruns its piece boundary
is exactly the kind of message that turns into an out-of-bounds panic if
it reaches an array unchecked.

A piece bitfield's bit order - piece 0 is the first byte's MSB - is
pinned by an exhaustive test rather than a couple of spot checks.
Getting this backwards produces no clear symptom (pieces just look
unavailable at seemingly random indices), so the test sets every bit in
a small bitfield individually and confirms only that exact index reads
back true.

The connection wraps a buffered reader specifically to avoid a syscall
per message. Without it, a message's 4-byte length prefix and its body
are two separate reads, each a user-space/kernel-space transition, and
across every message in a real download that adds up. The buffered
reader is explicitly single-goroutine-owned - not safe to share - which
lines up with each connection eventually belonging to exactly one
goroutine.

Message reads are tested against both ways TCP actually behaves, not
just the happy path of one message per read: two whole messages arriving
in a single underlying read, and a single message arriving split across
three separate writes. That's the exact class of bug that never
reproduces on a fast local network and shows up constantly against a
real, slightly slow peer. An unrecognized message ID parses successfully
rather than erroring, since `ReadMessage`'s job is framing - knowing
where a message ends - not understanding every ID that could appear on
the wire; rejecting unfamiliar IDs would drop the connection on any peer
using an extension message this client doesn't implement yet.

Manually verified against a real peer from a live swarm: handshake, a
full bitfield (3020/3020 pieces for the Debian torrent), a sent
`interested`, a received `unchoke` - a complete conversation exercising
every message type this layer adds, not just the pieces in isolation.

Accepting a connection is the mirror image of dialing one, not a
separate protocol. `Listener` runs one accept loop behind three
guards that all exist for the same reason `bencode`'s size caps and
`peer`'s message-length check do: an unbounded resource handed to
untrusted input becomes a denial of service. A cap on total accepted
connections, a cap per remote IP, and a short backoff after a run of
accept errors, so a peer that opens hundreds of connections or an
`fd`-exhaustion loop can't spend this process's file descriptors or
its CPU. `Accept` then runs the handshake in the opposite order from
`Dial`: an outgoing connection already knows which torrent it wants,
so it speaks first; an incoming one has no idea until the remote side
says so, so it listens first and only sends its own handshake back
once the info hash checks out. That ordering is also a small security
property, not just a protocol necessity: a mismatched info hash means
this client's peer ID and torrent metadata never went out over an
unverified connection at all.

`Accept` deliberately does not close the connection on error the way
`handshakeOver` does. `Dial` owns the connection it creates end to
end, so closing on failure is its job; an accepted connection is
already owned by `Listener`, which closes it in `release` the moment
the handler returns regardless of why. Having both close it would
just be redundant, not incorrect, but the ownership is cleaner with
exactly one closer.

### internal/piece

Piece and block boundary math, plus SHA-1 verification. Every function
is pure - bytes and integers in, a value out, no I/O - so the geometry
everything else depends on can be tested exhaustively on its own.

One set of geometry functions handles every place a piece or block
boundary is needed: building a request, validating an incoming block,
sizing a buffer. Duplicating that math at each call site would let an
off-by-one fix land in one place and silently miss the others. The last
piece's length gets treated as a first-class test case rather than an
afterthought - it's shorter than every other piece unless the total
length happens to divide evenly, and that exact-multiple case is the
trap, since a naive remainder calculation returns 0 for it instead of a
full piece.

`Work` (immutable) and `Progress` (mutable, single-goroutine) are
separate types rather than one struct. `Work` travels across the worker
channel and must never be mutated by two goroutines at once; keeping the
in-flight buffer and byte counters out of it entirely removes the
possibility instead of relying on discipline. SHA-1 verifies pieces
because the BitTorrent v1 spec requires it, not as a security choice -
it's cryptographically broken, and both the code and the README say so
explicitly, since it's the first thing a reviewer will flag.

### internal/download

The only package with full system visibility. It owns the coordinator,
every peer worker goroutine, and assembly of verified pieces into the
final file. Everything below it is mechanism; this is where policy
lives.

A single-piece download (interested, unchoke, pipelined block
requests, assemble, verify) is one function, reused unchanged by every
concurrent worker. Up to five block requests get pipelined per piece,
so round-trip latency overlaps across blocks instead of serializing
one request at a time behind it; the number is a fixed starting
point, flagged for later tuning. A choking peer resets in-flight
request bookkeeping immediately, since a choke silently drops every
request already sent, and anything still counted as in-flight has to
be treated as lost right away or the download stalls waiting on
answers that will never come. A piece download carries three
timeouts, not one: an overall cap, plus a shorter idle-read deadline
that resets on every block actually received, so a slow-but-progressing
peer survives and only a genuinely stalled one gets dropped.

Concurrency started as a plain work queue. One channel was pre-filled
with every piece, buffered to exactly the piece count so a worst-case
requeue storm couldn't deadlock, with one goroutine per peer pulling
from it. That design has an inherent ceiling, though. No single
component ever sees more than its own connection, so rarest-first
selection, duplicate prevention, and an endgame mode are all
structurally impossible on top of it. Concurrency now runs through a
coordinator instead: one goroutine owning every piece's status, its
availability across the swarm, the peer registry, and every in-flight
assignment, reached only through an `events` channel workers send to
and a per-peer `commands` channel it sends back on. No mutex guards
any of it, because no other goroutine ever touches that state. It's
the same *share memory by communicating* principle the rest of this
codebase already leans on, applied to the one place with genuinely
complex shared state.

Rarest-first selection exists for swarm health, not speed. Replicating
the pieces only one or two peers hold is what stops a torrent from
permanently losing data the moment its last holder disconnects. A
throughput improvement, if there is one, is a side effect, not the
goal. Duplicate requests are prevented by piece state alone
(missing/in-flight/complete) rather than a separate tracking
structure, and every assignment carries a timeout: a half-open TCP
connection, one where the peer is gone but the OS hasn't noticed,
would otherwise hold a piece hostage forever with no error and no
visible symptom beyond a stalled download. Near the end of a
download, duplicate prevention turns off on purpose. The last few
incomplete pieces get requested from every idle peer that has them,
and the first one to finish cancels the rest. Sending that
cancellation is not optional; a peer left sending blocks nobody needs
any more wastes the swarm's bandwidth, not just this client's.

Because piece-selection policy and connection mechanics are now fully
separate, the coordinator is tested as a pure state machine: events
in, commands out, no network, no timing, milliseconds per test. That
separation is worth more than it looks. The alternative, policy
tangled into the same code as socket I/O, would need fake peers and
real timeouts to test the same logic, which is the same trade-off
`bencode` and `metainfo` made by keeping parsing pure and I/O thin.

The worker rewrite this required turned each peer connection from one
goroutine into two. A worker can no longer just block on
`conn.ReadMessage()`, because it now also has to watch its `commands`
channel for whatever the coordinator sends. A small dedicated
`readLoop` goroutine does nothing but turn blocking reads into
channel sends, letting the worker's own `select` watch both sources
at once. The goroutine-leak test had to be re-run deliberately here:
doubling the goroutines per peer doubles the ways one of them could
fail to exit, and a leak that only shows up per connection is easy to
miss until it's multiplied by a swarm's worth of peers.

Two real bugs surfaced only once this ran against a live swarm rather
than fixtures. First, the stall-detection `select` re-created its
`time.After(stallTimeout)` timer on every trip through the loop, and
since a separate 1-second progress ticker fired first every time, that
30-second timer could never actually survive long enough to fire.
Reannounce-on-stall had silently never worked, on either design.
Second, `Progress.Rate()` only pruned its sample window when a new
sample arrived, so during a genuine stall it kept reporting whatever
it had last computed instead of admitting nothing recent had
happened. Both were fixed by moving the stall check onto the same
ticker that was already firing every second, and by having `Rate()`
return zero once its newest sample is older than its own window.

A further pair of bugs was specific to the coordinator refactor
itself, and both concerned endgame. Assigning a piece to an idle peer
initially picked the *first* incomplete piece that peer had, so every
idle peer converged on the same one piece while the rest of the
endgame set sat completely untouched, serializing exactly the phase
endgame exists to parallelize. And the trigger condition counted only
strictly-unassigned pieces, not ones already in flight, so endgame
could fire while far more than its threshold of pieces were actually
still outstanding, needlessly widening its own blast radius of
duplicate requests. Fixing both dropped measured duplicate piece
requests by roughly three quarters, same code, same day, nothing else
changed. See the README's performance comparison.

Progress counters are atomic wherever more than one goroutine touches
them: bytes, active and peak peers, connect stats, hash failures,
panics, duplicate assignments. Everything else is owned by
`Download`'s main goroutine alone (pieces done, the rate window), the
same ownership-over-locking principle applied throughout this
codebase. Progress is printed from exactly one place, a ticker inside
`Download`'s own select loop; workers never print directly, since a
hundred goroutines writing to stdout independently would produce
unreadable, interleaved output.

Resume marks pieces complete on the coordinator directly, before
`Run` starts, rather than filtering a work queue. `Download` calls
`storage.VerifyExisting` once at startup; anything already verified
gets marked in the `have` bitfield and handed to the coordinator's
`MarkComplete`, so it's simply never offered to any peer. There's no
"skip this piece" branch anywhere downstream. Resumed pieces still
have to be reported to `Progress` explicitly, though: the loop
counter (`completed`) starts at the resumed count correctly, but
`Progress.piecesDone` is a separate field that only advances when
`PieceCompleted` is called. Missing that call for resumed pieces was
a real bug caught during manual testing. The loop itself worked
fine, but `Percent()` and `ETA()` still read 0% at the start of a
resumed run, because two counters that need to move together are an
easy thing to under-update. The final throughput figure divides by
bytes actually transferred this session (`Progress.BytesDownloaded()`)
rather than the torrent's total size, for a related reason: a resumed
download's elapsed time only covers the pieces it actually fetched,
so dividing the whole file's size by that time would overstate
throughput by however much resume skipped.

Seeding did not need a second connection loop. A TCP connection is
bidirectional the moment its handshake finishes, so a peer this client
dialed to download from can just as legitimately request a piece back
over the same connection, and a peer that connected in to download
from this client can just as legitimately be asked for a piece it
holds. `worker` (dials) and `serveIncoming` (already accepted) both
just hand their connection to one shared `runConnection`, which makes
no distinction between the two once it starts. Every inbound message,
whichever direction the connection came from, funnels through one
`session.handleMessage`, deliberately singular, because an earlier
version handled messages in two different places (the idle loop and
the in-flight single-piece download loop), and the second one quietly
dropped every incoming block request and every choke/unchoke command
that arrived while a piece happened to be downloading. Consolidating
to one handler wasn't a style preference; it was the fix.

Choking reuses the coordinator rather than adding a second
single-owner goroutine next to it. The decision is the same shape as
piece selection: which commands to send which peers, over the same
per-peer channel `peerInfo` already holds. `recalcChoke` runs every
ten seconds, unchoking whichever four interested peers currently have
the best rate and choking everyone else; `rotateOptimistic` runs
independently every thirty, unconditionally unchoking one more
interested-but-choked peer so pure tit-for-tat's own deadlock (a peer
with nothing to offer never gets unchoked, so never gets the chance to
earn anything to offer) can't happen. Both write through one shared
`setChoked`, which only sends a command when a peer's choke state
actually changes and updates the coordinator's own record of it in the
same place, so a slow peer's full command channel can never leave the
coordinator's belief out of sync with what was actually sent. A
`RateTracker` (mutex-guarded, since it's genuinely written and read
from different goroutines, unlike the rest of the coordinator's state)
is the algorithm's whole input: a rolling window per peer per
direction, the same fixed-window approach `Progress.Rate` already
used, so a peer that was fast five minutes ago and has gone silent
since stops looking fast immediately rather than eventually. Once
`remainingPieceCount` reaches zero, `recalcChoke` switches from
sorting by download rate to upload rate, since download rate means
nothing once nothing is being requested.

Serving one incoming request is `serveRequest`, and the geometry trap
here is the mirror of the one `piece`'s tests already pin for
downloading: a request has to be bounds-checked against the piece it
actually names, not the torrent's standard piece length, because the
two only differ for the last, shorter piece, and the naive version
would wrongly accept an out-of-range offset there. The index has to be
read out of the raw payload before that length is even knowable, so
validation happens in two passes: peek the index, look up its real
length, then parse and bounds-check the rest against that. A request
larger than the standard 16 KB block gets rejected outright, the same
trust-boundary posture every untrusted input in this project gets. No
separate per-peer request queue exists on purpose: requests are served
synchronously, one at a time, on that peer's own connection goroutine,
so a peer that fires ten thousand requests has nowhere to queue them
in the first place, not a queue this client has to remember to cap.

A full audit after the choking algorithm first compiled and passed
its own unit tests found seeding did nothing end to end, silently.
Nine bugs, none of which produced an error: this client never sent
its own bitfield, so no peer had a reason to ever ask it for a block;
a leftover blocking wait for the remote peer's own unchoke gated the
entire connection loop, so a peer that connected purely to download
(and so had no reason to ever unchoke this client back) got dropped
after a timeout without being served a single block; incoming
requests and choke commands were dropped during an in-flight piece
download, described above; a bitfield sent as the very first message
after a handshake could be silently discarded by the same code that
treated it as "not a bitfield, ignore"; `readLoop` leaked one goroutine
per finished connection, because closing a socket doesn't wake a
goroutine already blocked sending its last message into a channel
nobody's reading from anymore; a single combined read/write deadline
let one direction's timeout quietly cut the other short, since the
reader and the connection's own writer are different goroutines
sharing one `net.Conn`; a peer that was interested could still never
be given work if it was also choking this client back, because nothing
checked that before assigning; an idle peer that asked for work at the
one moment every piece it held was already in flight elsewhere never
got asked again once one came free, since nothing re-offers work on
its own; and a duplicate bitfield from the same peer double-counted
every piece it held into swarm-wide availability. None of the nine had
a failing test pointing at them; the whole class only became visible
by driving a real coordinator over a real connection and checking that
a leecher which never reciprocates still gets served, which is now a
permanent regression test rather than a one-time finding.

Real tracker reporting replaced placeholder `uploaded=0`/`downloaded=0`
values with the running totals `Progress` already tracked, plus the
`started`/`completed`/`stopped` events the spec expects at exactly
those three moments and no others. The `left` field needed its own
counter rather than reusing `Progress.BytesDownloaded`, which only
counts bytes fetched over the network this session: a torrent resumed
from already-complete data would otherwise report "everything is still
missing" to the tracker despite having the whole file, since resumed
bytes never touch that counter. A separate `bytesOwned`, incremented
for both resumed and freshly-downloaded pieces, is what `left` is
computed from instead. A pure seeder resumed from complete data
originally announced `started` exactly once and then never again,
which is invisible until the one tracker that ever heard about it
loses its state (a restart, a crash) and no new leecher can find this
client until the process itself restarts. Routine re-announcing at the
tracker's own suggested interval now runs in both the downloading and
the seeding phase, independent of the download phase's own
stall-triggered early re-announce.

The client was verified against Transmission, a real and independent
implementation, in both directions: Transmission downloaded a complete
file from this client, and this client downloaded a complete file from
Transmission, both checksums matching the original exactly. Testing
against itself specifically can't catch a bug that's wrong the same
way on both ends of the wire (a backwards bitfield bit order would
send and receive backwards identically and still pass); an independent
implementation is what actually certifies the wire format, not just
this client's own consistency with itself. One genuine, non-code
finding came out of running that test over loopback: Transmission
silently discards any peer address in `127.0.0.0/8`, treating it as
bogus, since a real tracker would never legitimately hand out a
loopback address for another machine's peer. That's correct behavior
on Transmission's part, not a bug on either side, and it only matters
for same-machine interop testing, where the fix is to advertise the
machine's real LAN address instead of localhost.

### internal/storage

Owns all on-disk I/O for a download: preallocating the output file,
writing each verified piece to its correct offset, and reading pieces
back, both for resume verification and, later, for seeding. It never
decides which piece to download next; that policy stays in `download`.

The output file is preallocated once, up front, to its full final size,
rather than grown piece by piece. That makes every later piece write a
plain positional write into already-sized space, and it fails fast if
the disk can't hold the file at all, well before a large download would
otherwise discover that at 99%. That preallocation is a sparse file, not
a real disk-block reservation - `Truncate` sets the file's size in
metadata, and disk blocks only get allocated as each region is actually
written. That's portable through the standard library alone; real
preallocation needs OS-specific syscalls (`fallocate`, `F_PREALLOCATE`,
`SetFileValidData`), which would cost the project's zero-dependency,
single-code-path approach for a guarantee that's rarely worth it in
practice. The trade-off is honest: a full disk gets discovered on the
write that actually hits it, not at file-creation time.

Buffered writes get synced to disk exactly once, at completion, never
per piece. `fsync` on every piece would serialize the whole download
behind disk latency, and a crash before that final sync just means
whatever wasn't yet flushed gets caught and re-downloaded by resume
verification anyway. Correctness comes from re-verifying on resume, not
from fsync discipline, which is exactly what makes skipping per-piece
fsync safe.

Resume keeps no separate metadata file recording completion. Two
designs were possible: track "pieces done" in a side file, or re-verify
against the data itself. A side file can drift out of sync with what's
actually on disk - a crash, a user editing the file, a disk error - and
that leaves two sources of truth that can disagree. Re-hashing every
piece against the `.torrent`'s expected hashes has no such failure mode:
whatever the data proves is correct by definition, because it's the same
check every downloaded piece already has to pass.

The resume scan itself is bounded-parallel rather than one goroutine per
piece. A torrent with tens of thousands of pieces would otherwise turn a
startup scan into a random-access I/O storm and spike memory with that
many in-flight read buffers at once. A semaphore sized to
`runtime.NumCPU()` keeps disk and SHA-1 work both busy without that. And
a missing file gets the same response as a wrong-size or corrupt one:
every piece treated as not-yet-downloaded. Guessing at a partial match
for a file that isn't even the right size risks reading out of bounds or
verifying against the wrong bytes entirely - the same all-or-nothing
caution this project applies to every other untrusted or unexpected
input.
