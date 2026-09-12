package download

// Result is a piece that has been downloaded and hash-verified.
type Result struct {
	Index int
	Data  []byte
}
