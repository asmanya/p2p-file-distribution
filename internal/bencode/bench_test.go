package bencode

import (
	"bytes"
	"os"
	"testing"
)

// BenchmarkDecodeStrict measures parser throughput on a real .torrent file - the piece-hash blob inside it is by
// far the largest single string DecodeStrict has to handle, so this is a realistic case, not a toy input.
func BenchmarkDecodeStrict(b *testing.B) {
	data, err := os.ReadFile("../../testdata/debian-13.6.0-amd64-netinst.iso.torrent")
	if err != nil {
		b.Fatalf("read fixture: %v", err)
	}
	b.SetBytes(int64(len(data)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if _, err := DecodeStrict(bytes.NewReader(data)); err != nil {
			b.Fatalf("DecodeStrict: %v", err)
		}
	}
}
