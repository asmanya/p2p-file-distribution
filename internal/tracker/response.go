package tracker

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net/netip"

	"github.com/asmanya/p2p-file-distribution/internal/bencode"
)

// AnnounceResponse holds the parsed fields of a tracker's announce response.
type AnnounceResponse struct {
	Interval    int64
	MinInterval int64
	Complete    int64
	Incomplete  int64
	Peers       []netip.AddrPort
}

// ParseAnnounceResponse decodes a tracker's bencoded HTTP response body into an AnnounceResponse, checking failure
// reason first.
func ParseAnnounceResponse(data []byte) (*AnnounceResponse, error) {
	v, err := bencode.DecodeStrict(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("tracker: decode response: %w", err)
	}
	dict, err := bencode.AsDictionary(v)
	if err != nil {
		return nil, fmt.Errorf("tracker: response is not a dictionary: %w", err)
	}

	// A failure reason means the tracker refused the request - no peers follow.
	if reason, err := dict.GetString("failure reason"); err == nil {
		return nil, fmt.Errorf("tracker: failure reason: %s", reason)
	}

	resp := &AnnounceResponse{}
	resp.Interval, _ = dict.GetInt("interval")
	resp.MinInterval, _ = dict.GetInt("min interval")
	resp.Complete, _ = dict.GetInt("complete")
	resp.Incomplete, _ = dict.GetInt("incomplete")

	peersVal, err := dict.Get("peers")
	if err != nil {
		return nil, fmt.Errorf("tracker: missing peers: %w", err)
	}
	peerBS, err := bencode.AsByteString(peersVal)
	if err != nil {
		return nil, fmt.Errorf("tracker: legacy peer list not supported: %w", err)
	}

	peers, err := decodeCompactPeers([]byte(peerBS))
	if err != nil {
		return nil, err
	}
	resp.Peers = peers

	return resp, nil
}

// decodeCompactPeers splits a compact peers blob into AddrPorts - each peer is 4 bytes IPv4 address + 2 bytes port,
// both big-endian.
func decodeCompactPeers(blob []byte) ([]netip.AddrPort, error) {
	if len(blob)%6 != 0 {
		return nil, fmt.Errorf("trackers: compact peers blob length %d is not a multiple of 6", len(blob))
	}

	peers := make([]netip.AddrPort, 0, len(blob)/6)
	for i := 0; i < len(blob); i += 6 {
		addr := netip.AddrFrom4([4]byte(blob[i : i+4]))
		port := binary.BigEndian.Uint16(blob[i+4 : i+6])
		peers = append(peers, netip.AddrPortFrom(addr, port))
	}

	return peers, nil
}
