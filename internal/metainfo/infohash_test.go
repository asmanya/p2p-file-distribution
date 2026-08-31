package metainfo

import (
	"encoding/hex"
	"testing"
)

func TestInfoHashSmallTorrent(t *testing.T) {
	tr, err := ParseFile("../../testdata/small.torrent")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	want := "d8722b27308f2e4178f37e6a4c38e561ddb601ea"
	if got := hex.EncodeToString(tr.InfoHash[:]); got != want {
		t.Errorf("InfoHash = %s, want %s", got, want)
	}
}

func TestInfoHashDebianTorrent(t *testing.T) {
	tr, err := ParseFile("../../testdata/debian-13.6.0-amd64-netinst.iso.torrent")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	want := "481b6e3617be4c88f96cb25e47c9d8272130071e"
	if got := hex.EncodeToString(tr.InfoHash[:]); got != want {
		t.Errorf("InfoHash = %s, want %s", got, want)
	}
}
