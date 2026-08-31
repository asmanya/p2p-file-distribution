package bencode

import (
	"bytes"
	"os"
	"reflect"
	"testing"
)

// TestRoundTripDecodeEncode is the real proof: If a real .torrent file survives decode+encode
// byte-for-byte, info-hash computation (re-encoding the info dict to hash it) is guaranteed correct.
func TestRoundTripDecodeEncode(t *testing.T) {
	files := []string{
		"../../testdata/small.torrent",
		"../../testdata/debian-13.6.0-amd64-netinst.iso.torrent",
	}
	for _, path := range files {
		t.Run(path, func(t *testing.T) {
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}

			v, err := DecodeStrict(bytes.NewReader(original))
			if err != nil {
				t.Fatalf("DecodeStrict: %v", err)
			}

			var buf bytes.Buffer
			if err := Encode(&buf, v); err != nil {
				t.Fatalf("Encode: %v", err)
			}

			if !bytes.Equal(buf.Bytes(), original) {
				t.Errorf("round-trip mismatch: encoded output differs from original bytes")
			}
		})
	}
}

func TestRoundTripEncodeDecode(t *testing.T) {
	values := []Value{
		Integer(42),
		Integer(-42),
		Integer(0),
		ByteString("spam"),
		ByteString(""),
		List{},
		List{ByteString("spam"), Integer(42)},
		List{List{ByteString("nested")}},
		Dictionary{},
		Dictionary{"foo": ByteString("bar"), "baz": Integer(1)},
		Dictionary{"a": List{Integer(1), Integer(2), Dictionary{"x": ByteString("y")}}},
	}
	for _, v := range values {
		var buf bytes.Buffer
		if err := Encode(&buf, v); err != nil {
			t.Fatalf("Encode(%#v): %v", v, err)
		}

		got, err := DecodeStrict(&buf)
		if err != nil {
			t.Fatalf("DecodeStrict: %v", err)
		}

		if !reflect.DeepEqual(got, v) {
			t.Errorf("round-trip mismatch: got %#v, want %#v", got, v)
		}
	}
}
