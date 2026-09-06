package tracker

import (
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	requestTimeout  = 15 * time.Second
	maxResponseSize = 1 << 20 // 1 MiB - a legitimate tracker response is tiny
	userAgent       = "p2pget/0.1"
)

// Client announces to HTTP trackers.
type Client struct {
	httpClient *http.Client
}

// NewClient returns a Client configured with a bounded request timeout.
func NewClient() *Client {
	return &Client{
		httpClient: &http.Client{Timeout: requestTimeout},
	}
}

// Announce sends a GET request to announceURL and returns the parsed response.
func (c *Client) Announce(announceURL string) (*AnnounceResponse, error) {
	req, err := http.NewRequest(http.MethodGet, announceURL, nil)
	if err != nil {
		return nil, fmt.Errorf("tracker: build request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tracker: announce request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tracker: unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return nil, fmt.Errorf("tracker: read response body: %w", err)
	}

	return ParseAnnounceResponse(body)
}

// Close releases any idle keep-alive connections this client is holding open. Call it once a Client is done
// being used - without it, idle HTTP connections (and the goroutines backing them) linger until Go's default
// idle-connection timeout, not immediately.
func (c *Client) Close() {
	c.httpClient.CloseIdleConnections()
}
