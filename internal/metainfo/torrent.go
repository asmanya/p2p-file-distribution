package metainfo

// FileEntry describes one file within a torrent. Single-file torrents are represented as a
// Files slice with exactly one entry (Path = Name, Length = TotalLength) - so adding multi-file
// support later never requires rewriting code that already consumes Torrent.
type FileEntry struct {
	Path   string
	Length int64
}

// Torrent is the clean, application-level representation of a parsed .torrent file - deliberately
// a different shape from the raw bencode tree it was parsed from.
type Torrent struct {
	Announce     string     // primary tracker
	AnnounceList [][]string // BEP-12 tiers; optional, fallback trackers

	InfoHash [20]byte // fixed-size array, not a slice

	Name         string // suggested filename - untrusted input, validated on parse
	PieceLength  int64
	TotalLength  int64
	PiecesHashes [][20]byte

	Files []FileEntry // always >= 1 entry, even for single-file torrents
}

// PieceCount returns the number of pieces in the torrent
func (t *Torrent) PieceCount() int {
	return len(t.PiecesHashes)
}

// LastPieceLength returns the length of the final piece. This is usually shorter than PieceLength
// - except when TotalLength divides evenly by PieceLength, in which case the last piece is a full
// piece, not zero.
func (t *Torrent) LastPieceLength() int64 {
	remainder := t.TotalLength % t.PieceLength
	if remainder == 0 {
		return t.PieceLength
	}
	return remainder
}
