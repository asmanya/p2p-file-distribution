# CLAUDE.md

Operating rules for this repository, read automatically by Claude Code at the
start of every session. This file is deliberately short — long always-on
rules cost context on every request. All deep material (architecture
rationale, every phase's step-by-step build instructions) lives in one place:
`docs/planning/PROJECT.md`. That file is intentionally not summarized here;
open it deliberately, with file tools, when a step actually needs it.

## 1. Read this first

**Before implementing any step in Phase N, open `docs/planning/PROJECT.md`
and read that phase's full section** — not a summary, not what a previous
session remembered. If `docs/planning/PROJECT.md` is missing, stop and ask
before proceeding. Do not proceed from general BitTorrent knowledge or a
guess at what the plan probably says — that is exactly the failure mode this
rule exists to prevent.

If anything here conflicts with `PROJECT.md`, `PROJECT.md` wins.

## 2. Project

A BitTorrent v1 client, implemented from the protocol specification in Go,
with zero third-party runtime dependencies. Built as a deep-dive into
concurrent systems design — every architectural decision is deliberate and
documented, prioritizing correctness and clarity over development speed.
Full context: `PROJECT.md`, Volume I.

## 3. Working agreement

1. **One step at a time** — only the step named. Do not get ahead of it,
   even when the next step seems obvious.
2. **Stop after every step.** Show the diff, summarize the change, wait for
   review. Never batch multiple steps into one unreviewed dump.
3. **Ask, don't assume**, on anything ambiguous — most decisions are already
   made in `PROJECT.md`.
4. **Tests ship with the code they test**, not in a separate pass after.
5. **Verify the Definition of Done independently** — run the tests, read the
   actual output — before reporting a step complete.
6. **Never claim something works without having run it.** State plainly what
   couldn't be verified automatically (e.g. "needs a live tracker — please
   confirm").

## 4. Hard constraints

- Zero third-party runtime dependencies. Test-only deps (`testify`) are the
  one exception.
- Standard library, and the modern option specifically: `log/slog` not
  logrus/zap, `flag` not cobra, `net/netip` not a hand-rolled struct.
- No reflection, no struct tags, for the bencode codec — rejected by design.
- Package boundaries are one-directional (see Architecture below) — never
  violated.
- Every network read/write carries a deadline. No exceptions.
- Every size derived from network input is guard-checked before it's used to
  allocate memory.

## 5. Architecture (locked)

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

`bencode` never references torrents. `metainfo` never touches the network or
disk. `peer` implements mechanism, never policy (which piece to request is
`download`'s job). `piece` has no I/O at all. `cmd` has no logic beyond
wiring. If new code doesn't match the one-line purpose in a package's
`doc.go`, treat that as a signal it's in the wrong place. Full rationale:
`PROJECT.md`, Volume I §1.

## 6. Concurrency

- Prefer channels; reach for a mutex only when state is genuinely simple
  (`sync/atomic` for a plain counter).
- **From Phase 9 onward, the coordinator's state carries zero mutexes** —
  single-owner goroutine, reached only through its channels. If a mutex
  feels necessary there, re-read `PROJECT.md` Phase 9 before proceeding.
- The coordinator never blocks: non-blocking sends to peers, no disk or
  network calls inside it.
- Every goroutine has an unambiguous exit path — `defer conn.Close()`
  immediately after a successful handshake.
- The work queue buffer is exactly the piece count, never smaller (a smaller
  buffer is a guaranteed deadlock in the worst case).
- Every in-flight state has a timeout.
- Workers never print directly — everything flows through a channel to the
  main goroutine.

## 7. Untrusted input

Guard, before allocating, at every boundary: bencode string/nesting length,
`.torrent` name (reject path traversal), tracker response size + timeout,
peer message length cap, payload index/offset bounds, incoming request size.
Malformed input returns an `error` — never a panic. Full table: `PROJECT.md`,
Volume I §7 and Phase 5.

## 8. Testing

Pure functions → table-driven tests. Parsers → fuzz tests, must never panic.
Wire protocol → `net.Pipe()` fake peers. Tracker → `httptest`, never a real
tracker. Coordinator → fed synthetic events, no network. Concurrency → a
goroutine-leak test plus `-race`, run multiple times. Computed values (info
hash, geometry) → checked against `testdata/EXPECTED.md`, never only against
the program's own other output.

**`go test -race` must be clean before any step is considered done.**

## 9. Commands

```bash
make test    # run tests
make race    # tests under the race detector
make lint    # golangci-lint
make check   # fmt-check + vet + lint + race — exactly what CI runs
make build   # produce the binary
make cover   # generate a coverage report
```

Run `make check` before reporting a step complete.

## 10. Git

One meaningful commit per step. Prefixes: `feat(bencode):`, `test(peer):`,
`refactor(download):`, `chore:`, `docs:`. Message states what changed and
why. Tag every phase (`phase-1-bencode`). The Phase 9 refactor happens on its
own branch, merged once the checksum still verifies.

## 11. Out of scope

Implementing later phases early. Adding a dependency without discussion.
Overriding a decision already made in `PROJECT.md`. Concurrency abstractions
nobody asked for. Inline magic numbers (belong in
`internal/download/config.go` from Phase 11 on, with a comment explaining the
value). Test fixtures are public-domain torrents only (Debian, Ubuntu,
Internet Archive, Blender Foundation releases) — never copyrighted content.
No `.iso`/`.img` files committed to the repo.

## 12. When something's broken

Debug bottom-up — each step rules out one layer before moving to the next:

1. Bencode round-trip test still passing?
2. Info hash matches `testdata/EXPECTED.md`?
3. Piece/block geometry tests passing, including last-piece/last-block?
4. Wire-format byte fixtures matching exactly?
5. Only once 1–4 are clean, look at the layer above.

Changing code at random wastes more time than it saves.

## 13. Documentation upkeep

At the end of every phase: add 1–2 lines to README's "Design decisions"
immediately, while the reasoning is fresh. Update the state-ownership table
in `docs/architecture.md` if new shared state was introduced. Record
measured numbers the moment they're captured — a before/after comparison
only holds up if the baseline was written down at the time.

## 14. Metrics integrity

Any performance number that appears in the README must be actually measured,
on this machine, this network — never estimated. If no number is available
yet, say so and measure it. Method: same torrent, same network, same time of
day, three runs, report the median.

## 15. Session start

1. Read this file.
2. Confirm `docs/planning/PROJECT.md` exists — if not, stop and ask.
3. `git log --oneline` plus the latest phase tag, to see where the project
   stands.
4. `make check`.
5. Wait for the next step to be named. Don't propose "the next logical step"
   unprompted.
6. Once a step is named, read its section of `PROJECT.md` in full before
   writing any code.

## 16. Reporting progress

After a step: what was built (one or two sentences), files changed, tests
added and their actual output (pasted, not paraphrased), anything that
couldn't be verified automatically, the commit made. Keep it short and
factual — it exists for fast review, not a narration of the process.

---

This file is kept equivalent to `.cursor/rules/project.mdc`, which serves the
same purpose for Cursor sessions — update both together.
