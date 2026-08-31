package metainfo

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/asmanya/p2p-file-distribution/internal/bencode"
)

// ParseFile reads and parses a .torrent file at path.
func ParseFile(path string) (*Torrent, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("metainfo: read file: %w", err)
	}

	return Parse(data)
}

// Parse decodes .torrent file bytes into a Torrent.
func Parse(data []byte) (*Torrent, error) {
	v, err := bencode.DecodeStrict(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("metainfo: decode: %w", err)
	}

	root, err := bencode.AsDictionary(v)
	if err != nil {
		return nil, fmt.Errorf("metainfo: top-level value is not a dictionary: %w", err)
	}

	t := &Torrent{}

	announce, err := root.GetString("announce")
	if err != nil {
		return nil, fmt.Errorf("metainfo: missing announce: %w", err)
	}
	t.Announce = announce

	if err := parseAnnounceList(root, t); err != nil {
		return nil, err
	}

	infoVal, err := root.Get("info")
	if err != nil {
		return nil, fmt.Errorf("metainfo: missing info dictionary: %w", err)
	}

	info, err := bencode.AsDictionary(infoVal)
	if err != nil {
		return nil, fmt.Errorf("metainfo: info is not a dictionary: %W", err)
	}

	if err := parseInfo(info, t); err != nil {
		return nil, err
	}

	if err := validate(t); err != nil {
		return nil, err
	}

	return t, nil
}

// announce-list is optional (BEP-12): a list of tiers, each a list of URLs.
func parseAnnounceList(root bencode.Dictionary, t *Torrent) error {
	al, err := root.Get("announce-list")
	if err != nil {
		return nil // absent is fine
	}
	tiers, err := bencode.AsList(al)
	if err != nil {
		return fmt.Errorf("metainfo: announce-list: %w", err)
	}

	for _, tierVal := range tiers {
		tierList, err := bencode.AsList(tierVal)
		if err != nil {
			return fmt.Errorf("metainfo: announce-list tier: %w", err)
		}
		var tier []string
		for _, urlVal := range tierList {
			url, err := bencode.AsByteString(urlVal)
			if err != nil {
				return fmt.Errorf("metainfo: announce-list url: %w", err)
			}
			tier = append(tier, string(url))
		}
		t.AnnounceList = append(t.AnnounceList, tier)
	}
	return nil
}

func parseInfo(info bencode.Dictionary, t *Torrent) error {
	name, err := info.GetString("name")
	if err != nil {
		return fmt.Errorf("metainfo: missing info.name: %w", err)
	}
	t.Name = name

	pieceLength, err := info.GetInt("piece length")
	if err != nil {
		return fmt.Errorf("metainfo: missing info.piece length: %w", err)
	}
	t.PieceLength = pieceLength

	if err := parsePieces(info, t); err != nil {
		return err
	}

	length, err := info.GetInt("length")
	if err != nil {
		return fmt.Errorf("metainfo: missing info.length (multi-file torrents not yet supported): %W", err)
	}
	t.TotalLength = length
	t.Files = []FileEntry{{Path: t.Name, Length: t.TotalLength}}

	return nil
}

// parsePieces splits the concatenated raw-binary pieces blob into 20-byte SHA-1 hashes.
func parsePieces(info bencode.Dictionary, t *Torrent) error {
	piecesVal, err := info.Get("pieces")
	if err != nil {
		return fmt.Errorf("metainfo: missing info.pieces: %w", err)
	}
	piecesBS, err := bencode.AsByteString(piecesVal)
	if err != nil {
		return fmt.Errorf("metainfo: info.pieces is not a byte string: %w", err)
	}
	pieces := []byte(piecesBS)
	if len(pieces)%20 != 0 {
		return fmt.Errorf("metainfo: pieces blob length %d is not a multiple of 20", len(pieces))
	}

	for i := 0; i < len(pieces); i += 20 {
		var hash [20]byte
		copy(hash[:], pieces[i:i+20])
		t.PiecesHashes = append(t.PiecesHashes, hash)
	}

	return nil
}

func validate(t *Torrent) error {
	if t.PieceLength <= 0 {
		return fmt.Errorf("metainfo: piece length must be positive, got %d", t.PieceLength)
	}
	if t.TotalLength <= 0 {
		return fmt.Errorf("metainfo: total length must be positive, got %d", t.TotalLength)
	}

	// ceil(TotalLength / PieceLength) - cross-check against actual piece count
	expected := (t.TotalLength + t.PieceLength - 1) / t.PieceLength
	if int64(t.PieceCount()) != expected {
		return fmt.Errorf("metainfo: piece count %d does not match expected %d for total length %d, piece length %d", t.PieceCount(), expected, t.TotalLength, t.PieceLength)
	}

	if err := validateName(t.Name); err != nil {
		return fmt.Errorf("metainfo: %w", err)
	}

	return nil
}

// validateName rejects anything that isnt't a safe, plain filename. name is untrusted - it comes straight from a .torrent file anyone could have authored,
// and gets used to construct an output file path in a later phase. A malicious name like "../../.ssh/authorized_key" must never reach that far.
func validateName(name string) error {
	if name == "" {
		return errors.New("name is empty")
	}
	if strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("name contains a path separator: %q", name)
	}
	if strings.HasPrefix(name, ".") {
		return fmt.Errorf("name starts with a dot: %q", name) // catches "." and ".." too
	}
	if filepath.IsAbs(name) {
		return fmt.Errorf("name is an absolute path: %q", name)
	}

	return nil
}
