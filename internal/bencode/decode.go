package bencode

import (
	"bufio"
	"io"
	"strconv"
)

// Decoder decodes bencode values from a stream.
type Decoder struct {
	r *bufio.Reader
}

// NewDecoder returns a Decoder that reads from r.
func NewDecoder(r io.Reader) *Decoder {
	return &Decoder{r: bufio.NewReader(r)}
}

// Decode reads and returns the next bencode Value from the stream.
func (d *Decoder) Decode() (Value, error) {
	b, err := d.r.Peek(1)
	if err != nil {
		return nil, ErrUnexpectedEOF
	}
	switch {
	case b[0] == 'i':
		return d.decodeInteger()
	case b[0] == 'l':
		return nil, ErrInvalidTypeMarker // list (for now)
	case b[0] == 'd':
		return nil, ErrInvalidTypeMarker // dict (for now)
	case b[0] >= '0' && b[0] <= '9':
		return nil, ErrInvalidTypeMarker // byte string (for now)
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
