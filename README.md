# p2p-file-distribution

A BitTorrent v1 client built from the protocol specification, in Go, with
zero third-party runtime dependencies.

[![CI](https://github.com/asmanya/p2p-file-distribution/actions/workflows/ci.yml/badge.svg)](https://github.com/asmanya/p2p-file-distribution/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/badge/Go-1.25%2B-00ADD8?logo=go)](go.mod)
[![License: MIT](https://img.shields.io/github/license/asmanya/p2p-file-distribution)](LICENSE)

Every layer — bencode parsing, the tracker protocol, the peer wire
protocol, and concurrent piece download — is implemented from the spec,
not a library. The goal is a client whose every decision can be
explained, not just imported.

## Status

Working end to end: given a `.torrent` file, the client announces to its
tracker, connects to peers concurrently, downloads and SHA-1-verifies
every piece, and reassembles the complete file. Verified against a real
public swarm — see [Performance](#performance).

Not yet built: writing to disk incrementally (the whole file is currently
held in memory), resumable downloads, seeding, and a CLI. See
[What's next](#whats-next).

## Quick start

```
placeholder — a runnable CLI arrives once flag parsing is built
```

## Architecture

Seven packages, dependencies pointing strictly downward:

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

`bencode` doesn't know torrents exist; `peer` implements the wire
protocol but never decides which piece to request; `download` is the
only package that sees the whole system. Full per-package rationale and
the state-ownership table: [`docs/architecture.md`](docs/architecture.md).

## Design highlights

- **No reflection, no struct tags, no third-party bencode library.** A
  sealed `Value` interface with four concrete types keeps every type
  switch in the codebase exhaustive by construction.
- **Every untrusted-input boundary guards size before allocating** — a
  bencode string length, a tracker response body, a peer message length —
  rejected before it costs memory, never after.
- **The 20-byte info hash is computed two independent ways and
  cross-checked**, so its correctness is a verified fact rather than an
  assumption resting on one code path.
- **Wire formats are pure serialize/parse functions**, tested against
  exact byte fixtures and fault-injected with `net.Pipe` — malicious and
  malformed peers are simulated deterministically, not hoped for.
- **Concurrent download is a buffered work queue, not a coordinator.**
  The queue is sized to exactly the piece count, which makes a
  worst-case requeue deadlock provably impossible rather than merely
  unlikely.
- **A worker's panic is recovered loudly** — full stack trace, running
  counter — so one misbehaving peer can't take the whole download down,
  and the bug still can't hide.
- **SHA-1 is used because BitTorrent v1 requires it, not because it's
  considered secure** — it's broken, and that's called out explicitly
  rather than left for a reviewer to flag.

More: [`docs/architecture.md`](docs/architecture.md).

## Performance

Measured on a real download of the Debian 13.6.0 netinst ISO (~755 MiB,
3,020 pieces) from the live public tracker and swarm — not a fixture,
not an estimate.

| Metric | Value |
|---|---|
| Total download time | 14m 32s |
| Average throughput | 886.6 KiB/s |
| Peak concurrent peer connections | 9 |
| Peer connection success rate | 24% (9 of 37 tracker-returned addresses) |
| Piece hash failures | 2 (peers sent bad data; both re-requested and verified) |
| Peak memory usage | ~1.0 GiB |

The connection success rate and memory figure are both expected at this
stage: a fifth to a third of tracker-returned peers being dead is normal
for a public swarm, and the whole file lives in memory until disk
streaming is added. Throughput and total time are this project's
"before" numbers for a planned rarest-first coordinator refactor.

## Testing

- **Bencode:** table-driven edge cases, byte-exact round-trip tests
  against real `.torrent` files, and a native Go fuzz target.
- **Metainfo:** malicious-filename rejection tests, plus a golden test
  against ground truth recorded independently via `transmission-show`.
- **Tracker:** `httptest`-based failure-mode tests (timeouts, oversized
  bodies, garbage responses); verified live against Debian's tracker.
- **Peer:** `net.Pipe` fault injection (bad handshakes, split/merged TCP
  reads, hostile length prefixes); verified against a live swarm,
  completing handshakes with real qBittorrent, Transmission, Deluge, and
  libtorrent peers.
- **Piece/download:** a fake-seeder state machine covering choke/resume,
  corruption, and timeouts; a goroutine-leak test; a simulated 5-peer
  swarm with disconnecting, corrupting, and slow peers; the full suite
  run repeatedly under the race detector.
- **End to end:** a complete ~755 MiB Debian ISO downloaded from the real
  swarm and verified against Debian's published SHA-256.

`make check` (format, vet, lint, race) is required to pass before any
change ships.

## What's next

- Stream verified pieces to disk instead of holding the whole file in
  memory, with resume support for interrupted downloads.
- A coordinator with rarest-first piece selection, measured against the
  work-queue baseline above.
- Seeding, a choking algorithm, and a real CLI with progress display.
- BitTorrent v2 / SHA-256 piece hashes, and real multi-file torrent
  support (the data model already anticipates it).
