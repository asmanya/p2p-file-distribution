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
	if _, err := d.r.ReadByte(); err != nil { // consume 'i'
		return 0, ErrUnexpectedEOF
	}

	s, err := d.r.ReadString('e')
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
	lenStr, err := d.r.ReadString(':')
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
	if _, err := io.ReadFull(d.r, buf); err != nil {
		return "", ErrUnexpectedEOF
	}

	return ByteString(buf), nil
}

// List decode
func (d *Decoder) decodeList() (List, error) {
	if _, err := d.r.ReadByte(); err != nil { // cosume 'l'
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
			_, _ = d.r.ReadByte()
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
	if _, err := d.r.ReadByte(); err != nil { // consume 'd'
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
			_, _ = d.r.ReadByte()
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
