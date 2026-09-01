package tracker

import "crypto/rand"

const peerIDPrefix = "-GO0001-"

// GeneratePeerID returns a fresh 20-byte peer ID, unique per session.
func GeneratePeerID() ([20]byte, error) {
	var id [20]byte
	copy(id[:], peerIDPrefix)

	if _, err := rand.Read(id[len(peerIDPrefix):]); err != nil {
		return [20]byte{}, err
	}
	return id, nil
}
