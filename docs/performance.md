# Performance

Two kinds of numbers live in this repository. The [README](../README.md) has
real-world, end-to-end numbers from actual downloads against a live public
swarm. This document is the other half: isolated micro-benchmarks and
profiles of individual functions, run locally and reproducibly, with no
network involved. Together they answer two different questions - "how fast
does this feel as a whole" and "where does the CPU and memory actually go."

Every number below was captured on this machine by running the commands
shown, not estimated.

## Micro-benchmarks

```
go test ./internal/bencode/... -bench=. -benchtime=2s -run=^$
go test ./internal/peer/...    -bench=. -benchtime=2s -run=^$
go test ./internal/piece/...   -bench=. -benchtime=2s -run=^$
go test ./internal/download/... -bench=BenchmarkSelectPieceFor -benchtime=2s -run=^$
```

| Benchmark | Result | What it measures |
|---|---|---|
| `BenchmarkDecodeStrict` | 23.4 µs/op, 2.6 GB/s | Bencode decode of a real `.torrent` file, piece-hash blob included |
| `BenchmarkMessageSerialize` | 2.3 µs/op, 7.1 GB/s | Building a wire-format `piece` message carrying a 16 KiB block |
| `BenchmarkReadMessage` | 2.6 µs/op, 6.2 GB/s | Parsing that same message back off the wire |
| `BenchmarkVerify` | 126.8 µs/op, 2.1 GB/s | SHA-1 hashing one 256 KiB piece |
| `BenchmarkSelectPieceFor` | 140.6 µs/op | Rarest-first selection over 50,000 pieces - roughly 15x more pieces than the largest torrent this client has actually downloaded |

The last one is the most interesting result of the four. Piece selection
scans every piece on every assignment - an O(n) design chosen on the
assumption that a torrent's piece count stays small enough for that not to
matter, rather than reaching for a priority queue up front. At 50,000
pieces, a single selection still takes well under a millisecond. That
assumption holds by a wide margin, and now it's a measurement instead of a
guess.

## Profiling a real download

Rather than profile a live download against the public internet - slow,
non-reproducible, and dependent on whatever peers happen to be online at the
time - these profiles come from a real, in-process download against a local
swarm of five simulated peers (some slow, one that sends corrupt data, one
that disconnects mid-transfer). It exercises the exact same code path a live
download does - the coordinator, every worker goroutine, piece
verification, disk writes - with nothing about the pipeline itself
mocked out.

```
go test ./internal/download/ -run TestDownloadLocalSwarm -count=60 \
    -cpuprofile=cpu.prof -memprofile=mem.prof
go tool pprof -top cpu.prof
go tool pprof -top -inuse_space mem.prof
```

### CPU

```
      flat  flat%   sum%        cum   cum%
    1.54s 54.61% 54.61%      1.57s 55.67%  runtime.cgocall
    0.30s 10.64% 65.25%      0.30s 10.64%  runtime.stdcall2
    0.16s  5.67% 70.92%      0.16s  5.67%  runtime.stdcall1
    0.07s  2.48% 73.40%      0.07s  2.48%  crypto/sha1.blockSHANI
```

Only 38% of wall-clock time was spent actually running code at all - the
rest was this process blocked waiting on the OS, which is exactly what an
I/O-bound program should look like. Of the CPU time that was spent, over
half went straight into `runtime.cgocall`/`stdcall`, the Windows syscall
boundary for socket reads and writes, and 14.5% went into `fsync`ing the
output file to disk. SHA-1 verification - the one place this client does
real computational work per piece - shows up, but at a fraction of the
syscall cost. Nothing in the coordinator's own selection or bookkeeping
logic registers as a hot path at all. The bottleneck here is exactly where
it should be: talking to the network and the disk, not this client's own
decision-making.

### Memory

```
      flat  flat%   sum%        cum   cum%
    1539kB 36.31% 36.31%     1539kB 36.31%  runtime.allocm
    1024kB 24.17% 60.48%     1024kB 24.17%  runtime.malg
     650kB 15.35% 75.84%      650kB 15.35%  compress/flate.(*compressor).init
```

After 60 complete downloads back to back, in-use heap sits at 4.2 MB -
almost entirely Go runtime and test-harness bookkeeping (goroutine stacks,
`go test -v`'s own log compression), with nothing left over from piece
buffers or connection state. That's the same story the
[README's memory numbers](../README.md#performance) tell on a real 755 MiB
download: pieces are streamed to disk and released, never accumulated, so
nothing here grows with file size or with how many downloads have already
run in this process.

### Goroutines

With 5 peer connections live at once, the process held 25 goroutines -
roughly the expected 2 per connection (the connection's own loop plus its
dedicated reader) plus the coordinator, the listener, and Go's own runtime
housekeeping. That scales linearly and predictably with peer count, not
with file size or piece count, which is what makes a 50-peer download and a
5-peer one equally safe to run.
