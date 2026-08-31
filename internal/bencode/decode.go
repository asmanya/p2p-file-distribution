package bencode

import (
	"bufio"
	"io"
	"strconv"
)

// Decoder decodes bencode values from a stream.
type Decoder struct {
	r     *bufio.Reader
	depth int
	pos   int64
}

// Span is a byte range [start, end) within the original input the Decoder read from.
type Span struct {
	Start, End int64
}

// NewDecoder returns a Decoder that reads from r.
func NewDecoder(r io.Reader) *Decoder {
	return &Decoder{r: bufio.NewReader(r)}
}

// Decode reads and returns the next bencode Value from the stream.
func (d *Decoder) Decode() (Value, error) {
	return d.decodeValue()
}

func (d *Decoder) decodeValue() (Value, error) {
	b, err := d.r.Peek(1)
	if err != nil {
		return nil, ErrUnexpectedEOF
	}
	switch {
	case b[0] == 'i':
		return d.decodeInteger() // integer
	case b[0] == 'l':
		return d.decodeList() // list
	case b[0] == 'd':
		return d.decodeDictionary() // dict
	case b[0] >= '0' && b[0] <= '9':
		return d.decodeByteString() // byte string
	default:
		return nil, ErrInvalidTypeMarker
	}
}

// Integer decoder
func (d *Decoder) decodeInteger() (Integer, error) {
	if _, err := d.readByte(); err != nil { // consume 'i'
		return 0, ErrUnexpectedEOF
	}

	s, err := d.readString('e')
	if err != nil {
		return 0, ErrUnexpectedEOF // no closing 'e' found — truncated
	}
	s = s[:len(s)-1] // drop trailing 'e'

	if err := validateIntegerString(s); err != nil {
		return 0, err
	}

	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, ErrInvalidInteger
	}
	return Integer(n), nil
}

func validateIntegerString(s string) error {
	if s == "" {
		return ErrInvalidInteger
	}
	digits := s
	neg := s[0] == '-'
	if neg {
		digits = s[1:]
	}
	if digits == "" {
		return ErrInvalidInteger
	}
	if neg && digits == "0" {
		return ErrInvalidInteger // "-0"
	}
	if len(digits) > 1 && digits[0] == '0' {
		return ErrInvalidInteger // leading zero
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return ErrInvalidInteger // non-numeric
		}
	}
	return nil
}

// Bytestring Decoder
func (d *Decoder) decodeByteString() (ByteString, error) {
	lenStr, err := d.readString(':')
	if err != nil {
		return "", ErrUnexpectedEOF
	}
	lenStr = lenStr[:len(lenStr)-1] // drop trailing ':'

	length, err := strconv.ParseInt(lenStr, 10, 64)
	if err != nil || length < 0 {
		return "", ErrInvalidStringLen
	}

	if length > MaxStringLength {
		return "", ErrLimitExceeded // checked BEFORE allocating
	}

	buf := make([]byte, length)
	if err := d.readFull(buf); err != nil {
		return "", ErrUnexpectedEOF
	}

	return ByteString(buf), nil
}

// List decode
func (d *Decoder) decodeList() (List, error) {
	if _, err := d.readByte(); err != nil { // cosume 'l'
		return nil, ErrUnexpectedEOF
	}

	d.depth++
	defer func() { d.depth-- }()
	if d.depth > MaxNestingDepth {
		return nil, ErrLimitExceeded
	}

	list := List{}
	for {
		b, err := d.r.Peek(1)
		if err != nil {
			return nil, ErrUnexpectedEOF // unterminated
		}
		if b[0] == 'e' {
			_, _ = d.readByte()
			return list, nil
		}
		v, err := d.decodeValue()
		if err != nil {
			return nil, err
		}
		list = append(list, v)
	}
}

// dictionary decode
func (d *Decoder) decodeDictionary() (Dictionary, error) {
	if _, err := d.readByte(); err != nil { // consume 'd'
		return nil, ErrUnexpectedEOF
	}

	d.depth++
	defer func() { d.depth-- }()
	if d.depth > MaxNestingDepth {
		return nil, ErrLimitExceeded
	}

	dict := Dictionary{}
	var prevKey string
	first := true

	for {
		b, err := d.r.Peek(1)
		if err != nil {
			return nil, ErrUnexpectedEOF
		}
		if b[0] == 'e' {
			_, _ = d.readByte()
			return dict, nil
		}

		keyVal, err := d.decodeValue()
		if err != nil {
			return nil, err
		}

		keyBS, ok := keyVal.(ByteString)
		if !ok {
			return nil, ErrNonStringKey
		}
		key := string(keyBS)

		if !first && key <= prevKey {
			return nil, ErrUnsortedKeys // catches unsorted AND duplicate
		}
		first = false
		prevKey = key

		value, err := d.decodeValue()
		if err != nil {
			return nil, err
		}

		dict[key] = value
	}
}

// DecodeStrict reads exactly one bencode value from r and returns ErrTrailingData if
// any bytes remians afterward. Use this for inputs that must be a single, complete value.
// e.g. - a .torrent file
func DecodeStrict(r io.Reader) (Value, error) {
	d := NewDecoder(r)
	v, err := d.decodeValue()
	if err != nil {
		return nil, err
	}
	if _, err := d.r.Peek(1); err == nil {
		return nil, ErrTrailingData // more bytes exist after the value
	}
	return v, nil
}

// readByte, readString and readFull wrap the underlying reader's consuming operations, tracking exactly how many
// bytes have been consumed so far - needed for span recording. Peek is unaffected since it doesn't consume anything.
func (d *Decoder) readByte() (byte, error) {
	b, err := d.r.ReadByte()
	if err == nil {
		d.pos++
	}
	return b, err
}

func (d *Decoder) readString(delim byte) (string, error) {
	s, err := d.r.ReadString(delim)
	d.pos += int64(len(s))
	return s, err
}

func (d *Decoder) readFull(buf []byte) error {
	n, err := io.ReadFull(d.r, buf)
	d.pos += int64(n)
	return err
}

func (d *Decoder) decodeDictionaryWithSpans() (Dictionary, map[string]Span, error) {
	if _, err := d.readByte(); err != nil { // consume 'd'
		return nil, nil, ErrUnexpectedEOF
	}
	d.depth++
	defer func() { d.depth-- }()
	if d.depth > MaxNestingDepth {
		return nil, nil, ErrLimitExceeded
	}

	dict := Dictionary{}
	spans := map[string]Span{}
	var prevKey string
	first := true

	for {
		b, err := d.r.Peek(1)
		if err != nil {
			return nil, nil, ErrUnexpectedEOF
		}
		if b[0] == 'e' {
			_, _ = d.readByte()
			return dict, spans, nil
		}

		keyVal, err := d.decodeValue()
		if err != nil {
			return nil, nil, err
		}
		keyBS, ok := keyVal.(ByteString)
		if !ok {
			return nil, nil, ErrNonStringKey
		}
		key := string(keyBS)
		if !first && key <= prevKey {
			return nil, nil, ErrUnsortedKeys
		}
		first = false
		prevKey = key

		start := d.pos
		value, err := d.decodeValue()
		if err != nil {
			return nil, nil, err
		}
		spans[key] = Span{Start: start, End: d.pos}
		dict[key] = value
	}
}

// DecodeWithSpans decodes a top-level bencode dictionary and also returns
// the exact byte range each key's value occupied in the original input —
// so a caller can hash the original bytes of one value directly, instead
// of a re-encoded copy of it.
func (d *Decoder) DecodeWithSpans() (Dictionary, map[string]Span, error) {
	b, err := d.r.Peek(1)
	if err != nil {
		return nil, nil, ErrUnexpectedEOF
	}
	if b[0] != 'd' {
		return nil, nil, ErrInvalidTypeMarker
	}
	return d.decodeDictionaryWithSpans()
}

// AtEnd reports whether there is no more data left to read.
func (d *Decoder) AtEnd() bool {
	_, err := d.r.Peek(1)
	return err != nil
}
