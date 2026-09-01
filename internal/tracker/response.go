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

	var peers []netip.AddrPort
	switch pv := peersVal.(type) {
	case bencode.ByteString:
		peers, err = decodeCompactPeers([]byte(pv))
	case bencode.List:
		peers, err = decodeLegacyPeers(pv)
	default:
		err = fmt.Errorf("tracker: peers is neither a byte string nor a list")
	}
	if err != nil {
		return nil, err
	}
	resp.Peers = peers

	return resp, nil
}

// decodeLegacyPeers handles the non-compact fallback some trackers still use: a list of dictionaries, each with "ip"
// and "port" keys.
func decodeLegacyPeers(list bencode.List) ([]netip.AddrPort, error) {
	peers := make([]netip.AddrPort, 0, len(list))
	for _, v := range list {
		d, err := bencode.AsDictionary(v)
		if err != nil {
			return nil, fmt.Errorf("tracker: legacy peer entry is not a dictionary: %w", err)
		}

		ipStr, err := d.GetString("ip")
		if err != nil {
			return nil, fmt.Errorf("tracker: legacy peer missing ip: %w", err)
		}
		port, err := d.GetInt("port")
		if err != nil {
			return nil, fmt.Errorf("tracker: legacy peer missing port: %w", err)
		}

		addr, err := netip.ParseAddr(ipStr)
		if err != nil {
			return nil, fmt.Errorf("tracker: legacy peer invalid ip %q: %w", ipStr, err)
		}
		peers = append(peers, netip.AddrPortFrom(addr, uint16(port)))
	}

	return peers, nil
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
