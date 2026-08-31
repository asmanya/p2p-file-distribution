package bencode

import (
	"bytes"
	"os"
	"reflect"
	"strings"
	"testing"
)

func FuzzDecode(f *testing.F) {
	seeds := []string{
		"i42e", "i-42e", "i0e",
		"4:spam", "0:",
		"le", "l4:spami42ee", "ll4:spamee",
		"de", "d3:bar4:spam3:fooi42ee",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	for _, path := range []string{
		"../../testdata/small.torrent",
		"../../testdata/debian-13.6.0-amd64-netinst.iso.torrent",
	} {
		if data, err := os.ReadFile(path); err == nil {
			f.Add(string(data))
		}
	}

	f.Fuzz(func(t *testing.T, input string) {
		// decoding the input
		v, err := NewDecoder(strings.NewReader(input)).Decode()
		if err != nil {
			return // errors are fine - only panics are bugs
		}

		// encoding the input again (checking round-trip)
		var buf bytes.Buffer
		if err := Encode(&buf, v); err != nil {
			t.Fatalf("Encode of a succesfully decoded value failed: %v", err)
		}

		// decoding the new encoded value
		v2, err := NewDecoder(&buf).Decode()
		if err != nil {
			t.Fatalf("re-decode of encoded value failed: %v", err)
		}

		if !reflect.DeepEqual(v, v2) {
			t.Fatalf("round-trip mismatch: %#v != %#v", v, v2)
		}
	})
}
