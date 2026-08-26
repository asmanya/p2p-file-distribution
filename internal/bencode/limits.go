package bencode

const (
	MaxStringLength = 32 << 20 // 32MB
	MaxNestingDepth = 100
	MaxInputSize    = 64 << 20 // 64 MB
)
