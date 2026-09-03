# p2p-file-distribution

A BitTorrent v1 client, implemented from the protocol specification in Go, with zero third-party runtime dependencies.

[![CI](https://github.com/asmanya/p2p-file-distribution/actions/workflows/ci.yml/badge.svg)](https://github.com/asmanya/p2p-file-distribution/actions/workflows/ci.yml)

**Status:** actively in development. The bencode codec, `.torrent` metainfo
parsing, the tracker client, and the full peer wire protocol up through
the handshake and message exchange are all done. A `.torrent` file parses
into a validated, typed struct with a cross-checked info hash; the tracker
client turns that into a live list of peer addresses; and this client can
now open a TCP connection to a real peer, complete the handshake, exchange
bitfields, and hold a live choke/unchoke/interested conversation over a
buffered connection. Still to come: requesting and assembling actual piece
data.

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
- **Handshake serialization and parsing are pure functions**, with no
  socket involved at all. The wire format can be verified against known
  byte fixtures in a plain unit test; the network layer that exchanges
  those bytes is a thin, separate piece built on top, and its own tests
  swap in an in-memory connection instead of a real one.
- **A peer connection carries one deadline for the whole handshake, and
  the deadline is explicitly cleared once it succeeds.** Forgetting the
  second half is the classic version of this bug: a stale deadline set
  during connection setup fires hours later, in the middle of unrelated
  work, and looks like a completely different failure.
- **A peer that fails to connect or complete the handshake is an expected
  outcome, not a bug to chase.** Most of the addresses a tracker returns
  belong to peers that are offline, behind NAT, or gone — in practice,
  over half of them. The client treats connection-refused, handshake
  timeout, protocol mismatch, and info-hash mismatch as distinct,
  logged outcomes rather than one opaque failure, so a low success rate
  is legible instead of alarming.
- **A keep-alive gets its own sentinel message ID instead of being
  represented as `nil`.** Returning `nil, nil` from a read function is a
  well-known Go footgun — every caller has to remember to check for it,
  and the one place that forgets is a nil-pointer panic waiting to
  happen, especially once many connections are running concurrently. A
  sentinel keeps keep-alive as just another case in an exhaustive switch.
- **A piece bitfield's bit order is pinned down with an exhaustive test**,
  not spot-checked. Getting piece-0-is-the-MSB backwards produces no
  obvious symptom — pieces just look randomly unavailable — so every bit
  in a small bitfield is set and checked individually rather than trusting
  a couple of hand-picked examples to catch a systematic ordering bug.
- **Every payload parser validates a peer's numbers against this
  torrent's actual geometry before trusting them** — piece index against
  piece count, block offset and length against piece length. A peer's
  message is input, not fact; skipping this is exactly how a single
  malformed `request` turns into an out-of-bounds array access.
- **The connection is wrapped in a buffered reader specifically so message
  framing doesn't cost a syscall per read.** A message's 4-byte length and
  its body would otherwise be two separate reads each; multiplied across
  every message in a real download, that's a measurable amount of
  avoidable kernel transitions for no benefit.
- **Reading a message never assumes it lines up with one TCP read.** TCP
  is a byte stream with no message boundaries of its own — two messages
  can arrive in a single read, or one message can arrive split across
  several. Both are tested directly, because on a real network this isn't
  an edge case, it's what happens whenever a peer is even slightly slow.
- **An unrecognized message ID is parsed and returned, not rejected.**
  Framing only needs to know how long a message is, not what every ID
  means — rejecting anything unfamiliar would drop the connection over
  perfectly normal extension messages this client doesn't implement yet.

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

The peer handshake has byte-fixture tests for serialization (known input,
exact expected 68 bytes) and parsing (round-trip, truncated input, wrong
protocol string, empty input). The connection layer is tested with
`net.Pipe`, standing in a fake peer that can misbehave in ways a real
network never reproduces on demand — sending a mismatched info hash,
a wrong protocol string, half a handshake before disconnecting, or
nothing at all. It's also been run against real peers from a live
BitTorrent swarm: over a third completed a full handshake, returning
real peer IDs identifying live qBittorrent, Transmission, Deluge, and
libtorrent clients — the rest failed the way real peers normally do
(refused, timed out, or reset), exactly the profile the design expects.

The message protocol is covered at two levels. Pure tests check every
message type's exact byte encoding, a round-trip through serialize and
parse, and an exhaustive bitfield bit-ordering check (every index set and
checked individually, not sampled). Stream-level tests then exercise
framing the way a real TCP connection actually behaves: keep-alives mixed
into a real message sequence, a hostile length prefix rejected before
allocation, a truncated payload, two messages arriving in a single read,
and a single message arriving split across three separate writes. It's
also been run against a real peer from a live swarm: handshake, a full
bitfield (3020/3020 pieces), an `interested` sent, and an `unchoke` back —
a complete, live conversation using every message type this phase adds.

## What I'd do differently

*(placeholder — filled in at the end, as a retrospective)*
