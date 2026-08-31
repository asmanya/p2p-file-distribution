package bencode

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestDecodeInteger(t *testing.T) {
	tests := []struct {
		input   string
		want    Integer
		wantErr bool
	}{
		{"i42e", 42, false},
		{"i-42e", -42, false},
		{"i0e", 0, false},
		{"i-0e", 0, true},
		{"i03e", 0, true},
		{"ie", 0, true},
		{"i42", 0, true},
		{"iabce", 0, true},
	}
	for _, tt := range tests {
		d := NewDecoder(strings.NewReader(tt.input))
		v, err := d.Decode()
		if tt.wantErr {
			if err == nil {
				t.Errorf("input %q: got nil error, error wanted", tt.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("input %q: unexpected error %v", tt.input, err)
			continue
		}
		i, err := AsInteger(v)
		if err != nil || i != tt.want {
			t.Errorf("input %q: got (%v, %v), want %v", tt.input, i, err, tt.want)
		}
	}
}

func TestDecodeByteString(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    ByteString
		wantErr bool
	}{
		{"basic", "4:spam", "spam", false},
		{"empty", "0:", "", false},
		{"truncated", "3:ab", "", true},
		{"negative length", "-1:x", "", true},
		{"limit exceeded", "99999999999:x", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewDecoder(strings.NewReader(tt.input))
			v, err := d.Decode()
			if tt.wantErr {
				if err == nil {
					t.Errorf("input %q: got nil error, want error", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("input %q: unexpected error %v", tt.input, err)
			}
			bs, err := AsByteString(v)
			if err != nil || bs != tt.want {
				t.Errorf("input %q: got (%v, %v), want %v", tt.input, bs, err, tt.want)
			}
		})
	}
}

func TestDecodeByteStringRawBinary(t *testing.T) {
	raw := make([]byte, 20)
	for i := range raw {
		raw[i] = byte(i * 13) // non-UTF-8-safe bytes, like a real piece hash
	}
	input := "20:" + string(raw)

	d := NewDecoder(strings.NewReader(input))
	v, err := d.Decode()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	bs, err := AsByteString(v)
	if err != nil {
		t.Fatalf("AsByteString: %v", err)
	}
	if string(bs) != string(raw) {
		t.Errorf("bytes not preserved exactly: got %v, want %v", []byte(bs), raw)
	}
}

func TestDecodeList(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    List
		wantErr bool
	}{
		{"empty", "le", List{}, false},
		{"basic", "l4:spami42ee", List{ByteString("spam"), Integer(42)}, false},
		{"nested", "ll4:spamee", List{List{ByteString("spam")}}, false},
		{"unterminated", "l4:spam", nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewDecoder(strings.NewReader(tt.input))
			v, err := d.Decode()
			if tt.wantErr {
				if err == nil {
					t.Errorf("got nil error, wanted error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			l, err := AsList(v)
			if err != nil {
				t.Fatalf("AsList: %v", err)
			}
			if !reflect.DeepEqual(l, tt.want) {
				t.Errorf("got %#v, want %#v", l, tt.want)
			}
		})
	}
}

func TestDecodeDictionary(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    Dictionary
		wantErr error
	}{
		{"empty", "de", Dictionary{}, nil},
		{"basic", "d3:bar4:spam3:fooi42ee", Dictionary{"bar": ByteString("spam"), "foo": Integer(42)}, nil},
		{"unsorted", "d3:foo3:bar3:abc3:xyze", nil, ErrUnsortedKeys},
		{"duplicate", "d3:foo3:bar3:foo3:baze", nil, ErrUnsortedKeys},
		{"non-string key", "di42e3:fooe", nil, ErrNonStringKey},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewDecoder(strings.NewReader(tt.input))
			v, err := d.Decode()
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("got %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			dict, err := AsDictionary(v)
			if err != nil {
				t.Fatalf("AsDictionary: %v", err)
			}
			if !reflect.DeepEqual(dict, tt.want) {
				t.Errorf("got %#v, want %#v", dict, tt.want)
			}
		})
	}
}

func TestDecodeDepthLimit(t *testing.T) {
	input := strings.Repeat("l", MaxNestingDepth+1) + strings.Repeat("e", MaxNestingDepth+1)
	d := NewDecoder(strings.NewReader(input))

	if _, err := d.Decode(); !errors.Is(err, ErrLimitExceeded) {
		t.Errorf("got %v, want ErrLimitExceeded", err)
	}
}
