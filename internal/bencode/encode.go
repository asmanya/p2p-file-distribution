package bencode

import (
	"fmt"
	"io"
	"sort"
)

// Encode writes v to w in canonical bencode form
func Encode(w io.Writer, v Value) error {
	switch val := v.(type) {
	case ByteString:
		return encodeByteString(w, val)
	case Integer:
		return encodeInteger(w, val)
	case List:
		return encodeList(w, val)
	case Dictionary:
		return encodeDictionary(w, val)
	default:
		return ErrInvalidTypeMarker
	}
}

func encodeByteString(w io.Writer, s ByteString) error {
	_, err := fmt.Fprintf(w, "%d:%s", len(s), string(s))
	return err
}

func encodeInteger(w io.Writer, n Integer) error {
	_, err := fmt.Fprintf(w, "i%de", int64(n))
	return err
}

func encodeList(w io.Writer, l List) error {
	if _, err := io.WriteString(w, "l"); err != nil {
		return nil
	}
	for _, item := range l {
		if err := Encode(w, item); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, "e")
	return err
}

func encodeDictionary(w io.Writer, d Dictionary) error {
	keys := make([]string, 0, len(d))
	for k := range d {
		keys = append(keys, k)
	}
	sort.Strings(keys) // byte-wise ascending - Go's default comparison

	if _, err := io.WriteString(w, "d"); err != nil {
		return err
	}
	for _, k := range keys {
		if err := Encode(w, ByteString(k)); err != nil {
			return err
		}
		if err := Encode(w, d[k]); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, "e")
	return err
}
