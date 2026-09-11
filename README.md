# p2p-file-distribution

A BitTorrent v1 client written in Go from the protocol spec, with no
third-party runtime dependencies. Bencode, the tracker protocol, the
peer wire protocol, concurrent piece download, and disk I/O are all
built by hand rather than imported.

[![CI](https://github.com/asmanya/p2p-file-distribution/actions/workflows/ci.yml/badge.svg)](https://github.com/asmanya/p2p-file-distribution/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/badge/Go-1.25%2B-00ADD8?logo=go)](go.mod)
[![License: MIT](https://img.shields.io/github/license/asmanya/p2p-file-distribution)](LICENSE)

## Status

Works end to end. Give it a `.torrent` file and it announces to the
tracker, connects to peers concurrently, and picks which piece to
request next by rarity across the whole swarm rather than per
connection. Every piece is verified against its SHA-1 hash and streamed
straight to disk at constant memory. Near the end of a download, the
last few pieces get requested from every peer that has them, and
whichever finishes first cancels the rest. An interrupted download
resumes instead of starting over. Real numbers from actual downloads
are in [Performance](#performance).

Not built yet: seeding, choking, and a CLI. See
[What's next](#whats-next).

## Quick start

```
placeholder, arrives with the CLI
```

## Architecture

Seven packages. Dependencies only point down the list:

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

`bencode` has never heard of a torrent. `peer` speaks the wire protocol
but has no say in which piece gets requested next. `download` is the
only package that sees the whole system. Per-package reasoning and who
owns what shared state live in
[`docs/architecture.md`](docs/architecture.md).

## Design highlights

A few decisions worth calling out here; the rest are in the
architecture doc.

- No reflection or struct tags for bencode, and no library for it
  either. A sealed `Value` interface with four concrete types keeps
  every type switch in the codebase exhaustive by construction.
- Anything untrusted gets its size checked before it's allocated: a
  bencode string length, a tracker response body, a peer message.
  Rejected before it costs memory, not after.
- The info hash is computed two independent ways and cross-checked, so
  its correctness doesn't rest on a single code path.
- Wire formats are pure serialize/parse functions, tested against
  exact byte fixtures and fault-injected over `net.Pipe`. Malicious
  peers get simulated, not hoped for.
- Concurrent downloading is owned by a single goroutine. Every piece's
  status, its rarity across the swarm, the peer registry, and every
  in-flight assignment live inside one coordinator, reached only
  through channels. Nothing needs a mutex, because nothing outside
  that goroutine ever touches this state.
- Rarest-first piece selection exists for swarm health, not raw
  speed. Replicating the pieces only one or two peers hold is what
  stops a torrent from silently losing data the moment its last
  holder disconnects. A speed win, if there is one, is a side effect.
- Duplicate requests for the same piece are prevented by the piece's
  own state, and every assignment carries a timeout, so a half-open
  connection that's gone silent can't hold a piece hostage forever.
- That same duplicate prevention turns off on purpose near the end of
  a download. The last few pieces get requested from every peer that
  has them, and the first one to finish cancels the rest, closing the
  "stuck on one slow peer" tail without waiting it out.
- Piece-selection policy and connection mechanics are fully separate,
  so the policy gets tested by feeding it events directly: no
  network, no timing, no fake peers, the whole suite in milliseconds.
- A worker's panic gets recovered, loudly (full stack trace, a
  running counter), so one bad peer can't take the whole download
  down and the bug still doesn't go unnoticed.
- SHA-1 verifies pieces because that's what the BitTorrent v1 spec
  uses, not because it's still considered secure. It's broken, and
  that's said outright instead of leaving it for a reviewer to point
  out.
- Piece writes are positional (`WriteAt`) and don't need a lock.
  Every piece owns its own byte range, and non-overlapping writes are
  safe to run concurrently.
- Resume keeps no separate metadata file. It re-hashes whatever's on
  disk against the torrent's expected hashes on startup: a match
  means done, anything else gets re-downloaded. Nothing falls out of
  sync, because nothing but the file itself is trusted.

## Performance

Measured on a real download of the Debian 13.6.0 netinst ISO (~755
MiB, 3,020 pieces) from the live public tracker and swarm. Not a
fixture, not an estimate.

| Metric | Value |
|---|---|
| Peak memory, 755 MiB download | **9.4 MiB** (was ~1.0 GiB before disk streaming) |
| Peak memory, 1.5 MB download | 3.2 MiB, same order of magnitude despite a ~500x size difference |
| Total download time | 14m 32s |
| Average throughput | 886.6 KiB/s |
| Peak concurrent peer connections | 9 |
| Peer connection success rate | 24% (9 of 37 tracker-returned addresses) |
| Piece hash failures | 2 (peers sent bad data, both re-requested and verified) |

That ~100x memory drop is what streaming pieces to disk instead of
buffering the whole file actually gets you. A 500x larger file barely
moving peak memory is the real evidence that usage tracks in-flight
pieces, not file size. Dead or unreachable peers in a fifth to a third
of tracker responses is normal for a public swarm.

### Work queue vs. coordinator

Same Debian ISO, same live swarm, median of three runs each:

| Metric | Work queue (before) | Coordinator (after) |
|---|---|---|
| Total download time | 6m 33s | 10m 32s |
| Average throughput | 1969.8 KiB/s | 1224.2 KiB/s |
| Peak concurrent peers | 17 | 14 |
| Peer connection success rate | 46% | 38% |
| Duplicate/wasted piece requests | 0 (impossible by design) | 13 |

The wall-clock numbers moved the wrong way, reported as measured
rather than explained away. Three runs isn't enough to see past this
swarm's own noise: each design's three runs individually span well
over a minute of spread, and peer counts and connection success rates
swing by double digits run to run for reasons entirely outside this
client's control, namely which of the tracker's returned addresses
happened to be reachable at that exact moment. That's a bigger effect
than a piece-selection policy could plausibly have on one 755 MiB
download.

The one number here that isn't confounded by any of that: duplicate
piece requests fell from a median of 50 to 13 after fixing two real
bugs the refactor had introduced. One let duplicate end-of-download
requests pile onto a single piece instead of spreading across the
pieces still missing; the other triggered that end-of-download mode
earlier than it should have. Same code, same day, only those two
fixes changed. The case for the coordinator rests on that measurement
and on its fully deterministic unit tests, not on a throughput number
three live downloads against a public swarm can't honestly support.

## Testing

Bencode gets table-driven edge cases, byte-exact round-trip tests
against real `.torrent` files, and a native Go fuzz target seeded
with every known-bad case. Metainfo is checked against malicious
filenames and a golden test pinned to ground truth recorded
independently with `transmission-show`.

The tracker client has `httptest`-based tests for failures a real
tracker won't reproduce on demand (timeouts, oversized bodies,
garbage responses), plus a live run against Debian's tracker. Peer
handling is fault-injected over `net.Pipe`, covering bad handshakes,
split and merged TCP reads, and hostile length prefixes, and has
completed real handshakes with qBittorrent, Transmission, Deluge, and
libtorrent peers in a live swarm.

Piece download is covered by a fake-seeder state machine
(choke/resume, corruption, timeouts), a goroutine-leak test, and a
simulated 5-peer swarm with peers that disconnect, corrupt, or drag
their feet. Storage gets out-of-order and concurrent positional
writes, the final short piece, and resume tested against a full,
partial, corrupted, missing, and wrong-size file. The coordinator's
piece-selection policy is tested by feeding it events directly:
rarest-first selection, duplicate prevention, peer churn, assignment
timeouts, and end-of-download cancellation, all deterministic and
network-free. The whole suite runs repeatedly under the race
detector.

And end to end: a complete ~755 MiB Debian ISO, downloaded from the
real swarm, interrupted mid-download and resumed on a second run,
verified against Debian's published SHA-256.

`make check`, format, vet, lint, race, has to pass before anything
ships.

## What's next

- Seeding, a choking algorithm, and a real CLI with a progress
  display.
- BitTorrent v2 / SHA-256 piece hashes, and actual multi-file torrent
  support (the data model already leaves room for it).
