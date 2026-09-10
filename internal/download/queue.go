package download

// Result is a piece that has been downloaded and hash-verified.
type Result struct {
	Index int
	Data  []byte
}

// resultsBufferSize is a small buffer so a worker sending a result doesn't have to wait for the main goroutine
// to be ready to receive it right away.
const resultsBufferSize = 16
