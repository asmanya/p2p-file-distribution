package metainfo

import (
	"bytes"
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
