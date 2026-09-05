package piece

import (
	"crypto/sha1"
	"fmt"
)

// ExpectedHashSize is the length in bytes of a SHA-1 digest
const ExpectedHashSize = sha1.Size

// SHA-1 is used here because the BitTorrent v1 spec requires it for piece verification, not because it's considered
// secure. SHA-1 is cryptographically broken - practical collisions exist. This is an interoperability constraint,
// not a security choice. (BitTorretn v2 uses SHA-256 instead.)

// Verify computes the SHA-1 hash of buf and reports whether it matches expectedHash. expectedHash must be exactly
// ExpectedHashSize bytes.
func Verify(buf, expectedHash []byte) (bool, error) {
	if len(expectedHash) != ExpectedHashSize {
		return false, fmt.Errorf("piece: expected hash must be %d bytes, got %d", ExpectedHashSize, len(expectedHash))
	}
	sum := sha1.Sum(buf)
	return sum == [ExpectedHashSize]byte(expectedHash), nil
}
