package bencode

import "fmt"

// Value is implemented by the four bencode types: ByteString, Integer,
// List, and Dictionary. The marker method is unexported so no other
// package can implement it, keeping type switches on Value exhaustive.
type Value interface {
	bencodeValue()
}

// Defining concrete types

// ByteString is a bencode byte string — raw bytes, not necessarily valid
// UTF-8 text (e.g. piece hashes). Never use the strings package's
// rune-based functions on this; treat it as bytes.
type ByteString string

// Integer is a bencode integer — 64-bit signed, negative values are valid.
type Integer int64

// List is an ordered sequence of bencode values.
type List []Value

// Dictionary is a bencode dictionary. Key order isn't preserved here —
// bencode requires keys to be sorted, so sorted order is canonical order,
// and encoding re-sorts regardless of map iteration order.
type Dictionary map[string]Value

// Defining method for each concrete type

func (ByteString) bencodeValue() {}
func (Integer) bencodeValue()    {}
func (List) bencodeValue()       {}
func (Dictionary) bencodeValue() {}

// AsByteString returns v as a ByteString, or ErrTypeMismatch if v is not one.
func AsByteString(v Value) (ByteString, error) {
	bs, ok := v.(ByteString)
	if !ok {
		return "", fmt.Errorf("%w: expected ByteString, got %T", ErrTypeMismatch, v)
	}
	return bs, nil
}

// AsInteger returns v as an Integer, or ErrTypeMismatch if v is not one.
func AsInteger(v Value) (Integer, error) {
	i, ok := v.(Integer)
	if !ok {
		return 0, fmt.Errorf("%w: expected Integer, got %T", ErrTypeMismatch, v)
	}
	return i, nil
}

// AsList returns v as a List, or ErrTypeMismatch if v is not one.
func AsList(v Value) (List, error) {
	l, ok := v.(List)
	if !ok {
		return nil, fmt.Errorf("%w: expected List, got %T", ErrTypeMismatch, v)
	}
	return l, nil
}

// AsDictionary returns v as a Dictionary, or ErrTypeMismatch if v is not one.
func AsDictionary(v Value) (Dictionary, error) {
	d, ok := v.(Dictionary)
	if !ok {
		return nil, fmt.Errorf("%w: expected Dictionary, got %T", ErrTypeMismatch, v)
	}
	return d, nil
}

// Get returns the value at key in d, or ErrKeyNotFound if key is absent.
func (d Dictionary) Get(key string) (Value, error) {
	v, ok := d[key]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrKeyNotFound, key)
	}
	return v, nil
}

// GetString returns the ByteString at key, converted to a Go string.
func (d Dictionary) GetString(key string) (string, error) {
	v, err := d.Get(key)
	if err != nil {
		return "", err
	}
	bs, err := AsByteString(v)
	if err != nil {
		return "", fmt.Errorf("key %q: %w", key, err)
	}
	return string(bs), nil
}

// GetInt returns the Integer at key, converted to a Go int64.
func (d Dictionary) GetInt(key string) (int64, error) {
	v, err := d.Get(key)
	if err != nil {
		return 0, err
	}
	i, err := AsInteger(v)
	if err != nil {
		return 0, fmt.Errorf("key %q: %w", key, err)
	}
	return int64(i), nil
}
