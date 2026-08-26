package bencode

import (
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
