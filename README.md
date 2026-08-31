# p2p-file-distribution

A BitTorrent v1 client, implemented from the protocol specification in Go, with zero third-party runtime dependencies.

[![CI](https://github.com/asmanya/p2p-file-distribution/actions/workflows/ci.yml/badge.svg)](https://github.com/asmanya/p2p-file-distribution/actions/workflows/ci.yml)

**Status:** actively in development. The bencode codec (the serialization
format `.torrent` files and tracker responses use) is done — both decoding
and canonical encoding — and survives malformed or hostile input without
panicking. Still to come: `.torrent`/metainfo parsing, the tracker client,
and the peer wire protocol.

## Demo

*(placeholder — demo GIF added once the client is feature-complete)*

## Quick start

*(placeholder — clone, build, run instructions added once the client is runnable)*

## Architecture

Seven layers, dependencies point downward only:

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

Full rationale: `docs/architecture.md`.

## Design decisions

- **The bencode `Value` interface is sealed** via an unexported marker method, so only this package's four types (byte string, integer, list, dictionary) can ever satisfy it — type switches on `Value` stay exhaustive everywhere else in the codebase.
- **Size guards are checked before allocating**, never after. A length prefix is validated against a fixed cap first; only then is a buffer of that size created — so a hostile "allocate a terabyte" length claim is rejected for free, before any memory is touched.
- **Bencode byte strings are treated as opaque bytes, never as text.** The decoder never validates or normalizes them as UTF-8, since piece hashes are raw binary — any implicit text handling here would silently corrupt every hash later in the pipeline.
- **Recursive decoding (lists, dictionaries) is bounded by an explicit depth counter.** Without it, a pathological input like `llllll...` would recurse until the call stack overflows — an unrecoverable crash in Go, not a catchable error.
- **Dictionary keys must be strictly ascending, or decoding fails.** Bencode's spec requires sorted keys; rejecting anything else outright means a malformed or corrupted `.torrent` file fails loudly right where the bad data is, instead of surfacing as a confusing hash mismatch several layers away.
- **The encoder sorts dictionary keys with plain byte-wise comparison, never a locale-aware or case-insensitive one.** This is the property the info-hash computation depends on entirely — get it wrong and the hash silently mismatches, with the real bug several layers removed from the symptom.

## Performance

*(placeholder — measured numbers only, added once there's something to measure)*

## Testing

Table-driven tests cover the bencode decoder's edge cases — malformed
integers, truncated/oversized byte strings, unterminated and deeply nested
lists/dictionaries, out-of-order and duplicate keys — plus a dedicated test
confirming raw non-UTF-8 binary data survives decoding untouched. Round-trip
tests decode and re-encode two real `.torrent` files (including a full
Debian installer image) and assert the output is byte-for-byte identical to
the original — the actual guarantee the info-hash computation relies on.
All tests run under the race detector (`make race`).

## What I'd do differently

*(placeholder — filled in at the end, as a retrospective)*
