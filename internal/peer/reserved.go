package peer

// Reserved-byte bit positions the BitTorrent extension mechanism defines. This client sends all eight bytes as
// zero (no extensions supported yet), but the position are documented here so an incoming peer's reserved bytes
// can be logged meaningfully, and so adding real support later is a one-line change instead of a research session.
const (
	// ReservedByte5Bit4 (byte index 5, bit 4) signals BEP-10 extension protocol support - PEX and metadata
	// exchange both run over it.
	ReservedByte5Bit4 = 1 << 4

	// ReservedLastByteBit0 (last byte, bit 0) signals BEP-5 DHT support.
	ReservedLastByteBit0 = 1 << 0

	// ReservedLastByteBit2 (last byte, bit 2) signals BEP-6 Fast extension.
	ReservedLastByteBit2 = 1 << 2
)
