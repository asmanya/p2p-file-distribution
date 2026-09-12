package tracker

import (
	"fmt"
	"net/url"
)

// Event values the spec defines. EventNone means an ordinary periodic re-announce - the "event" field is left off
// the request entirely rather than encoded as an empty string, per BEP 3: only a torrent's first announce, its
// completion, and a graceful exit carry an event at all.
const (
	EventNone      = ""
	EventStarted   = "started"
	EventCompleted = "completed"
	EventStopped   = "stopped"
)

// BuildAnnounceURL constructs a tracker announce URL. uploaded and downloaded are this session's real running
// totals, not placeholders - a tracker builds swarm-wide statistics from these numbers, and some (private
// trackers especially) enforce a upload/download ratio from them.
func BuildAnnounceURL(announce string, infoHash, peerID [20]byte, port int, uploaded, downloaded, left int64, event string) (string, error) {
	u, err := url.Parse(announce)
	if err != nil {
		return "", fmt.Errorf("tracker: invalid announce URL: %w", err)
	}

	q := u.Query()
	q.Set("info_hash", string(infoHash[:]))
	q.Set("peer_id", string(peerID[:]))
	q.Set("port", fmt.Sprintf("%d", port))
	q.Set("uploaded", fmt.Sprintf("%d", uploaded))
	q.Set("downloaded", fmt.Sprintf("%d", downloaded))
	q.Set("left", fmt.Sprintf("%d", left))
	q.Set("compact", "1")
	if event != EventNone {
		q.Set("event", event)
	}
	u.RawQuery = q.Encode()

	return u.String(), nil
}
