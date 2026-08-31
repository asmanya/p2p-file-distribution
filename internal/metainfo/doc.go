// Package metainfo parses .torrent files into a clean application struct and
// computes the info hash used to identify a torrent to trackers and peers. It
// never opens a socket and never writes to disk - only reads and parses.
package metainfo
