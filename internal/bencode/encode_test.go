package bencode

import (
	"bytes"
	"testing"
)

func TestEncodeByteString(t *testing.T) {
	var buf bytes.Buffer
	if err := Encode(&buf, ByteString("spam")); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if got := buf.String(); got != "4:spam" {
		t.Errorf("got %q, want %q", got, "4:spam")
	}
}

func TestEncodeInteger(t *testing.T) {
	tests := []struct {
		in   Integer
		want string
	}{
		{42, "i42e"},
		{-42, "i-42e"},
		{0, "i0e"},
	}
	for _, tt := range tests {
		var buf bytes.Buffer
		if err := Encode(&buf, tt.in); err != nil {
			t.Fatalf("Encode(%d): %v", tt.in, err)
		}
		if got := buf.String(); got != tt.want {
			t.Errorf("Encode(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestEncodeList(t *testing.T) {
	var buf bytes.Buffer
	if err := Encode(&buf, List{ByteString("spam"), Integer(21)}); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if got := buf.String(); got != "l4:spami21ee" {
		t.Errorf("got %q, want %q", got, "l4:spami21ee")
	}
}

func TestEncodeEmptyListAndDict(t *testing.T) {
	var buf bytes.Buffer
	if err := Encode(&buf, List{}); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if got := buf.String(); got != "le" {
		t.Errorf("empty list: got %q, want %q", got, "le")
	}

	buf.Reset()
	if err := Encode(&buf, Dictionary{}); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if got := buf.String(); got != "de" {
		t.Errorf("empty dict: got %q, want %q", got, "de")
	}
}

// TestEncodeDictionaryKeyOrder is the non-negotiable one catches the classic bug where an
// out-of-order encoded dict silently breaks info hash
func TestEncodeDictionaryKeyOrder(t *testing.T) {
	d := Dictionary{
		"zebra": Integer(1),
		"apple": Integer(2),
		"mango": Integer(3),
	}
	var buf bytes.Buffer
	if err := Encode(&buf, d); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	want := "d5:applei2e5:mangoi3e5:zebrai1ee" // ascending: apple, mango, zebra
	if got := buf.String(); got != want {
		t.Errorf("got %q, want %q (keys must be byte-wise ascending)", got, want)
	}
}

func TestEncodeBinaryByteString(t *testing.T) {
	raw := make([]byte, 20)
	for i := range raw {
		raw[i] = byte(i * 13)
	}
	var buf bytes.Buffer
	if err := Encode(&buf, ByteString(raw)); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if got, want := buf.String(), "20:"+string(raw); got != want {
		t.Errorf("binary bytes not preserved exactly")
	}
}
