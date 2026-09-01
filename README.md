# p2p-file-distribution

A BitTorrent v1 client, implemented from the protocol specification in Go, with zero third-party runtime dependencies.

[![CI](https://github.com/asmanya/p2p-file-distribution/actions/workflows/ci.yml/badge.svg)](https://github.com/asmanya/p2p-file-distribution/actions/workflows/ci.yml)

**Status:** actively in development. The bencode codec, `.torrent` metainfo
parsing, and the tracker client are all done. A `.torrent` file parses into
a validated, typed struct with a cross-checked info hash; the tracker
client turns that into a live list of peer addresses, verified end-to-end
against a real public tracker. Still to come: the peer wire protocol.

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
  third-party runtime dependencies is a hard constraint here, and bencode
  is small enough to fully own — every byte of the format the rest of the
  client depends on is something I can explain, not just import.
- **No reflection, no struct tags.** Decoding builds an explicit `Value`
  interface with four concrete types instead of unmarshaling into
  arbitrary structs. The type system catches mistakes a reflection-based
  decoder would only surface at runtime, if at all.
- **The `Value` interface is sealed** via an unexported marker method, so
  only four types can ever satisfy it — type switches on `Value` stay
  exhaustive everywhere, with no risk of a silently-unhandled fifth case.
- **Size and depth guards run before allocating or recursing, never
  after.** A hostile multi-gigabyte length prefix, or thousands of nested
  lists, is rejected before it costs any memory or stack space.
- **Byte strings are opaque bytes, never text.** The decoder never
  validates or normalizes them as UTF-8, since piece hashes are raw binary
  — any implicit text handling would silently corrupt them.
- **Dictionary keys are strictly ascending on decode, and byte-wise sorted
  on encode.** Both exist for one reason: the info-hash computation
  depends on re-encoding a dictionary into the exact same bytes every
  time. Get either side wrong and the symptom is a hash mismatch, layers
  away from the actual bug.
- **A parsed `.torrent` file is its own struct, not the raw bencode tree
  wearing a different hat.** One explicit parsing step connects the two,
  so bencode's generic, nested shape never leaks into the rest of the
  client.
- **Multi-file support is designed in now, though only single-file
  torrents are implemented.** The struct already models a torrent as a
  list of files; a single-file torrent is just a list with one entry.
  Real multi-file support later extends that list instead of rewriting
  every consumer that assumed exactly one file.
- **A torrent's filename is treated as untrusted input, because it is.**
  It's checked against path separators, `..` components, leading dots,
  and absolute paths before use — rejecting anything that isn't a plain,
  safe filename. Without this, a malicious torrent could use a name like
  `../../.ssh/authorized_keys` to write outside the download directory.
- **Piece count is cross-checked against total length and piece length on
  parse, not taken on faith.** An inconsistent claim means the file is
  corrupt or malicious, and is rejected immediately instead of causing a
  confusing failure much later.
- **The info hash is computed two independent ways and cross-checked**:
  once by re-encoding the parsed `info` dictionary canonically, and once
  by hashing the dictionary's original raw bytes directly (tracked via
  byte offsets recorded during decoding). Agreement between the two is
  what makes the first method's correctness a verified fact rather than
  an assumption — and keeps the door open to relaxing strict key-ordering
  later without ever risking a silently wrong hash.
- **Peer addresses are `net/netip.AddrPort`, not a hand-rolled struct.**
  It's comparable and usable as a map key at zero cost — both matter once
  duplicate peers need to be deduplicated — and it handles IPv4 and IPv6
  through the same type.
- **The HTTP client enforces three guards network code almost always
  forgets**: an explicit request timeout (Go's default client has none —
  a dead tracker would hang the program forever), a hard cap on how much
  of the response body gets read (an oversized or malicious body can't
  exhaust memory), and a status-code check before ever attempting to parse
  the body (a non-200 response is usually an HTML error page, not bencode).
- **A tracker announce tries every URL in `announce-list`, in order,
  before giving up.** Most real-world torrents list a UDP tracker first
  and an HTTP one further down; skipping unsupported schemes instead of
  failing outright is the difference between working against real
  torrents and only working against hand-built test fixtures.
- **The compact and legacy peer list formats are both handled**, decided
  by inspecting the actual type of the `peers` value rather than assuming
  the tracker honored `compact=1`. Real trackers don't always agree with
  the spec, and the parser has to survive that rather than crash on it.

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

Metainfo parsing is checked against both real `.torrent` fixtures and a
battery of malicious filename inputs, confirming the path-traversal check
rejects all of them. A golden test then asserts every parsed field — info
hash, announce URL, piece geometry, total length, name — against
ground-truth values recorded independently (via `transmission-show`, not
this project's own code) for both fixtures.

All tests run under the race detector (`make race`).

The tracker client has both unit tests (peer ID generation, announce URL
encoding against a golden string, compact and legacy peer decoding) and
integration tests against a local `httptest` server exercising failure
modes a real tracker can't be relied on to reproduce on demand — a garbage
body, a non-200 status, a slow response that must trigger the timeout, and
a body larger than the size cap. It's also been verified against a real
public tracker (Debian's), which returned a live list of peers for a real
torrent — the actual proof this layer works outside of tests.

## What I'd do differently

*(placeholder — filled in at the end, as a retrospective)*
