# Architecture

## Layers

Seven layers, dependencies point downward only — a package may depend on the
ones listed below it, and never on the ones above it.

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

- `bencode` never references torrents, pieces, or peers.
- `metainfo` never opens a socket or writes to disk.
- `peer` implements mechanism (sending/parsing messages), never policy (which
  piece to request next) — policy belongs to `download`.
- `piece` has no I/O at all — every function takes bytes in, returns a value.
- `cmd` has no logic beyond flag parsing and a single call into
  `internal/download`.

## State ownership

*(populated once shared mutable state is introduced, documenting who owns
it — a goroutine or a mutex, never ambiguous)*

| State | Owner | Notes |
|-------|-------|-------|
| — | — | — |

## Design notes

### internal/bencode

The lowest layer: bencode serialization, with no awareness of torrents,
pieces, or peers. Every other package's correctness eventually rests on
this one being both exhaustively type-safe and hostile-input-safe, since
`.torrent` files and tracker responses are the first untrusted data the
client ever touches.

- **Sealed `Value` interface.** The four concrete types (`ByteString`,
  `Integer`, `List`, `Dictionary`) satisfy `Value` through an unexported
  marker method, so no other package can add a fifth. Every type switch on
  `Value` elsewhere in the codebase can stay exhaustive without a `default`
  case masking a missed type — chosen deliberately over a reflection-based
  decoder, which would push that same class of mistake to runtime instead
  of the compiler.
- **Guards are checked before allocation, not after.** A string length or
  nesting depth is validated against a fixed cap before any buffer is
  created or any recursive call is made — an oversized length prefix or a
  deeply nested input is rejected for free, with no memory or stack cost.
- **Byte strings are opaque bytes, never text.** The decoder performs no
  UTF-8 validation or normalization, since piece hashes are raw binary; any
  implicit text handling would silently corrupt them.
- **Dictionary keys are strictly ascending on decode, and byte-wise sorted
  (never locale-aware) on encode.** Both sides of this are load-bearing:
  encoding must produce a canonical byte sequence for the info-hash
  computation to be reproducible, and decoding must reject anything that
  doesn't already satisfy that canonical form, so a malformed input fails
  where it occurs instead of as a mysterious hash mismatch elsewhere.
- **Round-trip and fuzz tests are the actual proof, not the unit tests.**
  Decoding and re-encoding both real `.torrent` fixtures byte-for-byte
  demonstrates the encoder is canonical and the decoder lossless — which is
  exactly what the info-hash computation depends on. A native Go fuzz
  target, seeded from every known edge case, then checks the same
  decode/encode pair holds under random and mutated input, and that
  malformed input always fails as an error, never a panic.

### internal/metainfo

Converts a parsed bencode tree into the flat, typed `Torrent` struct the
rest of the client actually works with, and computes the 20-byte info
hash that identifies a torrent to trackers and peers. No network, no disk
writes beyond reading the `.torrent` file itself.

- **Two representations, one explicit conversion.** The raw bencode tree
  and the application-level `Torrent` struct are deliberately different
  shapes — the tree is generic and nested, the struct is flat and specific
  to what a torrent needs. `Parse` is the single, explicit boundary
  between them, so the tree's general-purpose shape never leaks further
  into the client.
- **`InfoHash` is a fixed-size `[20]byte`, not a slice** — comparable with
  `==`, usable as a map key, and its size is a compile-time guarantee.
  Piece hashes (`[][20]byte`) follow the same reasoning: they get compared
  against freshly-computed hashes constantly once downloading starts.
- **Files are modeled as a list from day one**, even though only
  single-file torrents parse today. A single-file torrent is just a
  list of one `FileEntry` (`Path` = name, `Length` = total length).
  Adding real multi-file support later means extending that list, not
  reworking every piece of code — storage offsets included — that
  assumed exactly one file.
- **A torrent's `name` field is untrusted input and is validated before
  anything else touches it.** It's rejected outright if it's empty,
  contains a path separator, starts with a dot (catching `.` and `..`
  components), or is an absolute path. This exists specifically because
  the name will later be used to construct an output file path — without
  the check, a crafted `.torrent` could write outside the intended
  download directory via a name like `../../.ssh/authorized_keys`.
- **Piece count is derived and cross-checked, never trusted as given.**
  `ceil(TotalLength / PieceLength)` is compared against the actual number
  of piece hashes present; a mismatch means the file is corrupt or
  malicious and is rejected during parsing, not discovered mid-download.
- **The info hash is computed two independent ways.** Method A re-encodes
  the parsed `info` dictionary through the bencode encoder and hashes
  that. Method B hashes the dictionary's original bytes directly, located
  via byte offsets the decoder records while parsing (a narrow, justified
  amendment to `bencode` — it reports offsets generically, with no
  knowledge of what a caller does with them). `Parse` cross-checks that
  both agree before returning successfully.
  - Today, this cross-check can only ever pass: the decoder rejects
    non-canonically-ordered dictionaries outright (see the `bencode`
    section above), so Method A and Method B are structurally guaranteed
    to agree. Its real value is future-facing — it's what would let
    strict key-ordering be relaxed later (to accept real-world
    `.torrent` files that don't quite follow spec) without ever risking
    a silently wrong info hash.
- **A golden test pins every parsed field against ground truth recorded
  independently**, via `transmission-show` rather than this project's own
  code, for both fixture torrents. This is the one test in the suite that
  can catch a bug this project's own reasoning would reproduce and miss —
  and it should never be deleted.
