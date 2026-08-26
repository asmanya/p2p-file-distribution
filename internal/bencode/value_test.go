package bencode

import (
	"errors"
	"testing"
)

func TestAsByteString(t *testing.T) {
	if bs, err := AsByteString(ByteString("hello")); err != nil || bs != "hello" {
		t.Fatalf("got (%q, %v), want (\"hello\", nil)", bs, err)
	}
	if _, err := AsByteString(Integer(21)); !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("got %v, want ErrTypeMismatch", err)
	}
}

func TestAsInteger(t *testing.T) {
	if i, err := AsInteger(Integer(21)); err != nil || i != 21 {
		t.Fatalf("got (%d, %v), want (21, nil)", i, err)
	}
	if _, err := AsInteger(ByteString("x")); !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("got %v, want ErrTypeMismatch", err)
	}
}

func TestAsList(t *testing.T) {
	want := List{Integer(1), Integer(2)}
	if l, err := AsList(want); err != nil || len(l) != 2 {
		t.Fatalf("got (%v, %v), want (%v, nil)", l, err, want)
	}
	if _, err := AsList(Integer(21)); !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("got %v, want ErrTypeMismatch", err)
	}
}

func TestAsDictionary(t *testing.T) {
	want := Dictionary{"a": Integer(1)}
	if d, err := AsDictionary(want); err != nil || len(d) != 1 {
		t.Fatalf("got (%v, %v), want (%v, nil)", d, err, want)
	}
	if _, err := AsDictionary(Integer(21)); !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("got %v, want ErrTypeMismatch", err)
	}
}

func TestDictionaryGet(t *testing.T) {
	d := Dictionary{"key": Integer(1)}
	if v, err := d.Get("key"); err != nil || v != Integer(1) {
		t.Fatalf("got (%v, %v), want (1, nil)", v, err)
	}
	if _, err := d.Get("missing"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("got %v, want ErrKeyNotFound", err)
	}
}

func TestDictionaryGetString(t *testing.T) {
	d := Dictionary{"key": ByteString("value")}
	if s, err := d.GetString("key"); err != nil || s != "value" {
		t.Fatalf("got (%q, %v), want (\"value\", nil)", s, err)
	}
	if _, err := d.GetString("missing"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("got %v, want ErrKeyNotFound", err)
	}
	d2 := Dictionary{"key": Integer(21)}
	if _, err := d2.GetString("key"); !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("got %v, want ErrTypeMismatch", err)
	}
}

func TestDictionaryGetInt(t *testing.T) {
	d := Dictionary{"key": Integer(21)}
	if n, err := d.GetInt("key"); err != nil || n != 21 {
		t.Fatalf("got (%d, %v), want (21, nil)", n, err)
	}
	if _, err := d.GetInt("missing"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("got %v, want ErrKeyNotFound", err)
	}
	d2 := Dictionary{"key": ByteString("x")}
	if _, err := d2.GetInt("key"); !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("got %v, want ErrTypeMismatch", err)
	}
}
