package bencode

// Resource guards checked before allocating, so a hostile length or nesting depth is rejected before it costs memory.
const (
	MaxStringLength = 32 << 20 // 32MB
	MaxNestingDepth = 100
	MaxInputSize    = 64 << 20 // 64 MB
)
