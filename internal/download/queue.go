package download

import "github.com/asmanya/p2p-file-distribution/internal/piece"

// Result is a piece that has been downloaded and hash-verified
type Result struct {
	Index int
	Data  []byte
}

// resultsBufferSize is a small buffer so a worker sending a result doesn't have to wait for the main goroutine
// to be ready to receive it right away.
const resultsBufferSize = 16

// NewQueues builds the work and results channels for a concurrent download. The work channel is buffered to exactly
// len(work) and pre-filled with every piece - if every worker had to push a failed piece back onto it at once,
// any smaller buffer would deadlock.
func NewQueues(work []piece.Work) (workCh chan piece.Work, resultCh chan Result) {
	workCh = make(chan piece.Work, len(work))
	for _, w := range work {
		workCh <- w
	}
	resultCh = make(chan Result, resultsBufferSize)
	return workCh, resultCh
}
