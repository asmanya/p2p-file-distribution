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

*(placeholder — expanded as architectural decisions are made)*
