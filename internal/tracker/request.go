package tracker

import (
	"fmt"
	"net/url"
)

// BuildAnnounceURL constructs the tracker announce URL for a torrent's first announce (event=started).
func BuildAnnounceURL(announce string, infoHash, peerID [20]byte, port int, left int64) (string, error) {
	u, err := url.Parse(announce)
	if err != nil {
		return "", fmt.Errorf("tracker: invalid announce URL: %w", err)
	}

	q := u.Query()
	q.Set("info_hash", string(infoHash[:]))
	q.Set("peer_id", string(peerID[:]))
	q.Set("port", fmt.Sprintf("%d", port))
	q.Set("uploaded", "0")
	q.Set("downloaded", "0")
	q.Set("left", fmt.Sprintf("%d", left))
	q.Set("compact", "1")
	q.Set("event", "started")
	u.RawQuery = q.Encode()

	return u.String(), nil
}
