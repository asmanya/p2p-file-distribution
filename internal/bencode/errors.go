package bencode

import "errors"

var (
	ErrUnexpectedEOF     = errors.New("bencode: unexpected end of input")
	ErrInvalidTypeMarker = errors.New("bencode: invalid type marker")
	ErrInvalidInteger    = errors.New("bencode: invalid integer")
	ErrInvalidStringLen  = errors.New("bencode: invalid string length")
	ErrLimitExceeded     = errors.New("bencode: limit exceeded")
	ErrNonStringKey      = errors.New("bencode: non-string dictionary key")
	ErrUnsortedKeys      = errors.New("bencode: dictionary keys not sorted or duplicated")
	ErrTrailingData      = errors.New("bencode: trailing data after top-level value")
	ErrTypeMismatch      = errors.New("bencode: type mismatch")
)
