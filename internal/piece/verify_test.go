package piece

import "testing"

func TestVerify(t *testing.T) {
	data := []byte("hello, bittorrent")
	// SHA-1 of "hello, bittorrent", verified independently via sha1sum.
	correctHash := []byte{
		0x7d, 0x9a, 0xca, 0x00, 0x1b, 0x54, 0x46, 0x26,
		0x25, 0x61, 0xeb, 0xc7, 0x5f, 0xb6, 0xc0, 0xb9,
		0x95, 0x01, 0x25, 0x4c,
	}

	ok, err := Verify(data, correctHash)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("expected hash to match, got mismatch")
	}
}

func TestVerifyMismatch(t *testing.T) {
	data := []byte("hello, bittorrent")
	correctHash := make([]byte, ExpectedHashSize)
	copy(correctHash, []byte{0xff}) // deliberately wrong

	ok, err := Verify(data, correctHash)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected mismatch, got match")
	}
}

func TestVerifyEmptyBuffer(t *testing.T) {
	// Verifying an empty buffer must not panic - it should just compute the SHA-1 of zero bytes and compare normally.
	hash := make([]byte, ExpectedHashSize)
	if _, err := Verify(nil, hash); err != nil {
		t.Fatalf("unexpected error on empty buffer: %v", err)
	}
}

func TestVerifyBadHashLength(t *testing.T) {
	if _, err := Verify([]byte("data"), []byte{1, 2, 3}); err == nil {
		t.Fatal("expected error for wrong-length hash")
	}
}
