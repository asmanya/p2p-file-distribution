package metainfo

import "testing"

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
