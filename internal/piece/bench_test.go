package piece

import (
	"crypto/rand"
	"crypto/sha1"
	"testing"
)

// BenchmarkVerify measures SHA-1 throughput at a realistic piece size (256 KiB, a common real-world piece length)
// - this is the actual per-piece cost every downloaded piece pays before it's trusted enough to write to disk.
func BenchmarkVerify(b *testing.B) {
	const pieceLength = 256 * 1024
	buf := make([]byte, pieceLength)
	if _, err := rand.Read(buf); err != nil {
		b.Fatalf("rand.Read: %v", err)
	}
	hash := sha1.Sum(buf)

	b.SetBytes(pieceLength)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		ok, err := Verify(buf, hash[:])
		if err != nil || !ok {
			b.Fatalf("Verify: ok=%v err=%v", ok, err)
		}
	}
}
