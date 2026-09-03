// Package peer implements the BitTorrent wire protocol: pure encoding and decoding of handshakes and messages,
// plus a thin connection layer to exchange them over TCP. It has no download policy - deciding which piece to
// request next belongs to the download package, not here.
package peer

// Personal notes (not part of the package doc, just for my own understanding):
//
// File by file:
//   - handshake.go:    Handshake struct + Serialize()/ParseHandshake() - the 68-byte connection opener, pure functions.
//   - reserved.go:     documents the handshake's reserved-byte bit positions (DHT, extension protocol, fast extension).
//   - dial.go:         Dial() - TCP dial + handshake exchange + info hash check; handshakeOver() is the net.Pipe-testable half.
//   - message.go:      MessageID type, Message struct, ReadMessage()/Serialize() - the [length][ID][payload] wire framing.
//   - bitfield.go:      Bitfield type - HasPiece()/SetPiece()/Count(), one bit per piece, piece 0 = first byte's MSB.
//   - payload.go:       typed payload parsers (have/request/piece/port), each validating against piece count and piece length.
//   - limits.go:        MaxMessageLength, BlockSize, MaxIncomingRequestSize - the size caps referenced by message.go and payload.go.
//   - conn.go:          Conn - buffered reader + peer identity + choke/interested state, with deadline-guarded I/O methods.
