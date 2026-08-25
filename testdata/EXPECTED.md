# Expected values — ground truth

These values were produced independently by `transmission-create` /
`transmission-show`, not by this project's own code. They exist so that
each layer (info hash, piece geometry, etc.) can be checked against a
trusted external source instead of only against the program's own other
output.

## small.torrent (self-made fixture)

| Field | Value |
|---|---|
| File name | `sample.dat` |
| Info hash (SHA-1, hex) | `d8722b27308f2e4178f37e6a4c38e561ddb601ea` |
| Announce URL | `http://localhost:6969/announce` (placeholder — no real tracker yet) |
| Piece length | 32,768 bytes (32 KiB) |
| Piece count | 46 |
| Total length | 1,500,000 bytes |
| Last piece length | 25,440 bytes (partial — total isn't an exact multiple of the piece length) |

## debian-13.6.0-amd64-netinst.iso.torrent (real-world fixture)

| Field | Value |
|---|---|
| File name | `debian-13.6.0-amd64-netinst.iso` |
| Info hash (SHA-1, hex) | `481b6e3617be4c88f96cb25e47c9d8272130071e` |
| Announce URL | `http://bttracker.debian.org:6969/announce` |
| Piece length | 262,144 bytes (256 KiB) |
| Piece count | 3,020 |
| Total length | 791,674,880 bytes |
| Last piece length | 262,144 bytes (full-size — total length happens to be an exact multiple of the piece length) |
