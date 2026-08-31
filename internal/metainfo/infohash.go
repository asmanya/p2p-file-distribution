package metainfo

import (
	"bytes"
	"crypto/sha1"

	"github.com/asmanya/p2p-file-distribution/internal/bencode"
)

// computeInfoHash re-encodes the info directory canonically and returns its SHA-1 hash - the 20 bytes that identify
// a torrent to trackers and peers. This only produces the correct hash because the bencode encoder is canonical
// (sorted keys, byte-exact) and round-trip tested.
func computeInfoHash(info bencode.Dictionary) ([20]byte, error) {
	var buf bytes.Buffer
	if err := bencode.Encode(&buf, info); err != nil {
		return [20]byte{}, err
	}
	return sha1.Sum(buf.Bytes()), nil
}
