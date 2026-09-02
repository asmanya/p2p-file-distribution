// Package peer implements the BitTorrent wire protocol: pure encoding and decoding of handshakes and messages,
// plus a thin connection layer to exchange them over TCP. It has no download policy - deciding which piece to
// request next belongs to the download package, not here.
package peer
