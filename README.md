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

- **The bencode codec is hand-written, not pulled from a library.** Zero
  third-party runtime dependencies is a hard constraint for this project,
  and bencode is small enough to fully own — which also means every byte
  of the format the rest of the client depends on is something I can
  actually explain, not just import.
- **No reflection, no struct tags.** Decoding builds an explicit `Value`
  interface with four concrete types instead of unmarshaling into
  arbitrary Go structs. It's more typing up front, but the type system
  catches mistakes that a reflection-based decoder would only surface at
  runtime, if at all.
- **The `Value` interface is sealed** via an unexported marker method, so
  only this package's four types (byte string, integer, list, dictionary)
  can ever satisfy it — type switches on `Value` stay exhaustive everywhere
  else in the codebase, with no risk of a silently-unhandled fifth case.
- **Size and depth guards are checked before allocating or recursing, never
  after.** A string length is validated against a fixed cap before any
  buffer is created; nesting depth is checked before any recursive call.
  A hostile multi-gigabyte length prefix, or a few thousand nested lists,
  is rejected for free, before it costs any memory or stack space.
- **Byte strings are treated as opaque bytes, never as text.** The decoder
  never validates or normalizes them as UTF-8, since piece hashes are raw
  binary — any implicit text handling here would silently corrupt every
  hash later in the pipeline.
- **Dictionary keys must be strictly ascending on decode, and are sorted by
  plain byte comparison (never locale-aware) on encode.** Both sides of
  this exist for the same reason: the info-hash computation depends on
  re-encoding a dictionary into the exact same canonical bytes every time.
  Get either side wrong and the symptom shows up as a confusing hash
  mismatch, layers away from the actual bug.

## Performance

*(placeholder — measured numbers only, added once there's something to measure)*

## Testing

Table-driven tests cover the bencode decoder's edge cases — malformed
integers, truncated/oversized byte strings, unterminated and deeply nested
lists/dictionaries, out-of-order and duplicate keys — plus a dedicated test
confirming raw non-UTF-8 binary data survives decoding untouched.

Round-trip tests decode and re-encode two real `.torrent` files (including
a full Debian installer image) and assert the output is byte-for-byte
identical to the original — the actual guarantee the info-hash computation
relies on.

The decoder is also fuzz-tested: a native Go fuzz target runs it against
random and mutated input, seeded with every known-valid and known-malformed
case above, asserting it never panics and that anything it successfully
decodes survives an encode/decode round trip unchanged.

All tests run under the race detector (`make race`).

## What I'd do differently

*(placeholder — filled in at the end, as a retrospective)*
