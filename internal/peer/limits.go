package peer

const (
	// MaxMessageLength caps a message's declared length before allocation. Largest legit message is a piece:
	// a 16 KB block + 9-byte header. 128 KB is generous headroom.
	MaxMessageLength = 128 * 1024

	// BlockSize is the de-facto standard request size used by this client.
	BlockSize = 16 * 1024

	// MaxIncomingRequestSize caps how large a block a peer can request from us.
	MaxIncomingRequestSize = 16 * 1024
)
