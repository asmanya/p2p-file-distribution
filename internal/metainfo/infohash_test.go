package metainfo

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"os"
	"testing"

	"github.com/asmanya/p2p-file-distribution/internal/bencode"
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

func TestInfoHashMethodsCrossCheck(t *testing.T) {
	files := []string{
		"../../testdata/small.torrent",
		"../../testdata/debian-13.6.0-amd64-netinst.iso.torrent",
	}
	for _, path := range files {
		t.Run(path, func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}

			d := bencode.NewDecoder(bytes.NewReader(data))
			root, spans, err := d.DecodeWithSpans()
			if err != nil {
				t.Fatalf("DecodeWithSpans: %v", err)
			}

			infoVal, err := root.Get("info")
			if err != nil {
				t.Fatalf("Get(info): %v", err)
			}
			info, err := bencode.AsDictionary(infoVal)
			if err != nil {
				t.Fatalf("AsDictionary: %v", err)
			}

			methodA, err := computeInfoHash(info)
			if err != nil {
				t.Fatalf("computeInfoHash: %v", err)
			}
			methodB := sha1.Sum(data[spans["info"].Start:spans["info"].End])

			if methodA != methodB {
				t.Errorf("Method A (%x) and Method B (%x) disagree", methodA, methodB)
			}
		})
	}
}
