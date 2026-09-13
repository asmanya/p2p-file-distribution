package metainfo

import (
	"bytes"
	"crypto/sha1"
	"testing"

	"github.com/asmanya/p2p-file-distribution/internal/bencode"
)

func TestParseValidTorrent(t *testing.T) {
	tr, err := ParseFile("../../testdata/small.torrent")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if tr.Name != "sample.dat" {
		t.Errorf("Name: %q, want: %q", tr.Name, "sample.dat")
	}
	if tr.PieceCount() != 46 {
		t.Errorf("PieceCount() = %d, want 46", tr.PieceCount())
	}
}

func TestValidateName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid", "movie.mp4", false},
		{"empty", "", true},
		{"forward slash", "../etc/passwd", true},
		{"backslash", "..\\windows\\system32", true},
		{"dotdot", "..", true},
		{"leading dot", ".hidden", true},
		{"absolute unix", "/etc/passwd", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateName(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateName(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

// validTorrentDict returns a minimal, otherwise-valid single-piece torrent dictionary, so each announce-list test
// only has to vary the one field it's actually checking.
func validTorrentDict(announceList bencode.Value) bencode.Dictionary {
	data := "x"
	hash := sha1.Sum([]byte(data))
	info := bencode.Dictionary{
		"name":         bencode.ByteString("x"),
		"piece length": bencode.Integer(1),
		"pieces":       bencode.ByteString(string(hash[:])),
		"length":       bencode.Integer(1),
	}
	root := bencode.Dictionary{
		"announce": bencode.ByteString("http://x"),
		"info":     info,
	}
	if announceList != nil {
		root["announce-list"] = announceList
	}
	return root
}

func mustEncode(t *testing.T, v bencode.Value) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := bencode.Encode(&buf, v); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return buf.Bytes()
}

// TestParseAnnounceList covers BEP-12's optional multi-tracker field: absent entirely, a well-formed multi-tier
// list, and every shape of malformed input parseAnnounceList has to reject rather than panic on.
func TestParseAnnounceList(t *testing.T) {
	tests := []struct {
		name         string
		announceList bencode.Value
		wantErr      bool
		wantTiers    int
	}{
		{"absent is fine", nil, false, 0},
		{
			"valid multi-tier",
			bencode.List{
				bencode.List{bencode.ByteString("http://a"), bencode.ByteString("http://b")},
				bencode.List{bencode.ByteString("http://c")},
			},
			false, 2,
		},
		{"not a list", bencode.Integer(1), true, 0},
		{"tier not a list", bencode.List{bencode.Integer(1)}, true, 0},
		{"url not a string", bencode.List{bencode.List{bencode.Integer(1)}}, true, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tor, err := Parse(mustEncode(t, validTorrentDict(tt.announceList)))
			if (err != nil) != tt.wantErr {
				t.Fatalf("Parse() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if len(tor.AnnounceList) != tt.wantTiers {
				t.Errorf("AnnounceList has %d tiers, want %d", len(tor.AnnounceList), tt.wantTiers)
			}
		})
	}
}

func TestParseRejectsNonPositiveLengths(t *testing.T) {
	tests := []struct {
		name        string
		pieceLength int
		totalLength int
	}{
		{"zero piece length", 0, 25},
		{"negative piece length", -1, 25},
		{"zero total length", 10, 0},
		{"negative total length", 10, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := bencode.Dictionary{
				"name":         bencode.ByteString("x"),
				"piece length": bencode.Integer(tt.pieceLength),
				"pieces":       bencode.ByteString(string(make([]byte, 20))),
				"length":       bencode.Integer(tt.totalLength),
			}
			root := bencode.Dictionary{
				"announce": bencode.ByteString("http://x"),
				"info":     info,
			}
			if _, err := Parse(mustEncode(t, root)); err == nil {
				t.Error("expected an error, got nil")
			}
		})
	}
}

func TestParseRejectsInconsistentPieceCount(t *testing.T) {
	info := bencode.Dictionary{
		"name":         bencode.ByteString("x"),
		"piece length": bencode.Integer(10),
		"pieces":       bencode.ByteString(string(make([]byte, 20))), // only 1 piece hash
		"length":       bencode.Integer(25),                          // needs ceil(25/10) = 3
	}
	root := bencode.Dictionary{
		"annonce": bencode.ByteString("http://x"),
		"info":    info,
	}

	var buf bytes.Buffer
	if err := bencode.Encode(&buf, root); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if _, err := Parse(buf.Bytes()); err == nil {
		t.Errorf("expected error for inconsistent piece count, got nil")
	}
}
