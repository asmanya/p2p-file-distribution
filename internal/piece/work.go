package piece

// Work describes one piece to download. Immutable - it travels on a channel between peers, and must never be
// mutated in place.
type Work struct {
	Index        int
	ExpectedHash []byte
	Length       int64
}

// Progress tracks an in-progress download attempt for one piece. Mutable, but owned by exactly one goroutine
// for the lifetime of the attempt.
type Progress struct {
	Work       Work
	Buffer     []byte
	Downloaded int64
	Requested  int64
	Backlog    int
}
