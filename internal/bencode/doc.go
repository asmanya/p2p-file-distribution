// Package bencode implements encoding and decoding of the bencode
// serialization format used by the BitTorrent protocol. Bencode supports
// exactly four types: byte strings, integers, lists, and dictionaries
// (with string keys, sorted).
//
// This package has zero knowledge of torrents, pieces, or peers — it only
// understands the four bencode types above, nothing domain-specific.
package bencode
