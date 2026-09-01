package tracker

import (
	"errors"
	"fmt"
	"net/url"
)

// ErrUnsupportedScheme indicates a tracker URL whose scheme this client cannot announce to (e.g. UDP), distinct from
// a request that failed.
var ErrUnsupportedScheme = errors.New("tracker: unsupported scheme")

// AnnounceAll tries each tracker URL from the torrent's announce and announce-list, in order, returning the first
// successful response. A tracker that fails (bad URL, unsupported scheme, network error, bad response) is recorded
// in errs and skipped — it does not abort the whole attempt, since real torrents mix working and dead trackers.
func (c *Client) AnnounceAll(announce string, announceList [][]string, buildURL func(trackerURL string) (string, error)) (*AnnounceResponse, error) {
	urls := flattenTiers(announce, announceList)

	var errs []error
	for _, trackerURL := range urls {
		// A malformed URL can't even be inspected for its scheme.
		u, err := url.Parse(trackerURL)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", trackerURL, err))
			continue
		}

		// UDP (and anything else) is out of scope for this client — skip it, don't fail the whole announce.
		if u.Scheme != "http" && u.Scheme != "https" {
			errs = append(errs, fmt.Errorf("%s: %w: %s", trackerURL, ErrUnsupportedScheme, u.Scheme))
			continue
		}

		// buildURL fills in info_hash/peer_id/left/etc. for this specific tracker URL.
		announceURL, err := buildURL(trackerURL)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", trackerURL, err))
			continue
		}

		resp, err := c.Announce(announceURL)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", trackerURL, err))
			continue
		}

		// First tracker to succeed wins — no need to try the rest.
		return resp, nil
	}

	// Every tracker failed: report all of them together, not just the last one tried.
	return nil, fmt.Errorf("tracker: all trackers failed: %w", errors.Join(errs...))
}

// flattenTiers returns every tracker URL from announce-list (BEP-12: a list of tiers, each a list of URLs — all
// flattened into one ordered slice here), falling back to the single announce URL if announce-list is absent.
func flattenTiers(announce string, announceList [][]string) []string {
	if len(announceList) == 0 {
		return []string{announce}
	}

	var urls []string
	for _, tier := range announceList {
		urls = append(urls, tier...)
	}
	return urls
}
