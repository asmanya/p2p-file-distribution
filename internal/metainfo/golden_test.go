package metainfo

import (
	"encoding/hex"
	"testing"
)

// TestGoldenValues checks every parsed field against ground-truth values recorded independently (via transmission-show,
// not this project's own code) in testdata/EXPECTED.md. This is the one test that verifies against an external source
// of truth, not just the program's own internal consistency.
func TestGoldenValues(t *testing.T) {
	tests := []struct {
		name            string
		path            string
		wantInfoHash    string
		wantAnnounce    string
		wantPieceLength int64
		wantPieceCount  int
		wantTotalLength int64
		wantLastPiece   int64
		wantName        string
	}{
		{
			name:            "small.torrent",
			path:            "../../testdata/small.torrent",
			wantInfoHash:    "d8722b27308f2e4178f37e6a4c38e561ddb601ea",
			wantAnnounce:    "http://localhost:6969/announce",
			wantPieceLength: 32768,
			wantPieceCount:  46,
			wantTotalLength: 1500000,
			wantLastPiece:   25440,
			wantName:        "sample.dat",
		},
		{
			name:            "debian torrent",
			path:            "../../testdata/debian-13.6.0-amd64-netinst.iso.torrent",
			wantInfoHash:    "481b6e3617be4c88f96cb25e47c9d8272130071e",
			wantAnnounce:    "http://bttracker.debian.org:6969/announce",
			wantPieceLength: 262144,
			wantPieceCount:  3020,
			wantTotalLength: 791674880,
			wantLastPiece:   262144,
			wantName:        "debian-13.6.0-amd64-netinst.iso",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr, err := ParseFile(tt.path)
			if err != nil {
				t.Fatalf("ParseFile: %v", err)
			}

			if got := hex.EncodeToString(tr.InfoHash[:]); got != tt.wantInfoHash {
				t.Errorf("InfoHash = %s, want %s", got, tt.wantInfoHash)
			}
			if tr.Announce != tt.wantAnnounce {
				t.Errorf("Announce = %q, want %q", tr.Announce, tt.wantAnnounce)
			}
			if tr.PieceLength != tt.wantPieceLength {
				t.Errorf("PieceLength = %d, want %d", tr.PieceLength, tt.wantPieceLength)
			}
			if tr.PieceCount() != tt.wantPieceCount {
				t.Errorf("PieceCount() = %d, want %d", tr.PieceCount(), tt.wantPieceCount)
			}
			if tr.TotalLength != tt.wantTotalLength {
				t.Errorf("TotalLength = %d, want %d", tr.TotalLength, tt.wantTotalLength)
			}
			if got := tr.LastPieceLength(); got != tt.wantLastPiece {
				t.Errorf("LastPieceLength() = %d, want %d", got, tt.wantLastPiece)
			}
			if tr.Name != tt.wantName {
				t.Errorf("Name = %q, want %q", tr.Name, tt.wantName)
			}
		})
	}
}
