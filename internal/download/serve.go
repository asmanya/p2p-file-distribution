package download

import (
	"encoding/binary"
	"fmt"
	"net/netip"

	"github.com/asmanya/p2p-file-distribution/internal/peer"
	"github.com/asmanya/p2p-file-distribution/internal/piece"
	"github.com/asmanya/p2p-file-distribution/internal/storage"
)

// serveRequest handles one incoming block request from a peer we're uploading to: validates it, checks we're willing and able to
// serve it, reads the block from disk, and sends it back.
//
// A request we deliberately decline - the peer is choked, or we don't have the piece yet - is not an error and returns nil, the
// peer just never gets a reply for it, same as any real BitTorrent client would do. Only a real failure (malformed payload, disk
// error, write error) is returned, since those are only cases worth tearing down the connection over.
func serveRequest(conn *peer.Conn, addr netip.AddrPort, payload []byte, pieceCount int, pieceLength, totalLength int64, have *HaveBitfield, file *storage.File, progress *Progress, rates *RateTracker) error {
	if len(payload) != 12 {
		return fmt.Errorf("download: request payload length %d, want 12", len(payload))
	}
	index := int(binary.BigEndian.Uint32(payload[0:4]))
	if index < 0 || index >= pieceCount {
		return fmt.Errorf("download: request piece index %d out of range [0,%d)", index, pieceCount)
	}

	if conn.AmChoking {
		return nil // we're choking this peer right now - not a protocol violation, just ignore
	}
	if !have.Has(index) {
		return nil // we don't have this piece (yet)
	}

	pieceLen, err := piece.Length(index, pieceCount, pieceLength, totalLength)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}

	// Re-parse against this piece's real length now that we know it - the standard pieceLength would wrongly accept out-of-range
	// offsets against the last, shorter piece.
	req, err := peer.ParseRequestPayload(payload, pieceCount, int(pieceLen))
	if err != nil {
		return fmt.Errorf("download:  %w", err)
	}
	if req.Length > peer.MaxIncomingRequestSize {
		return fmt.Errorf("download: request length %d exceeds cap %d", req.Length, peer.MaxIncomingRequestSize)
	}

	block, err := file.ReadBlock(index, pieceCount, pieceLength, totalLength, int64(req.Begin), int64(req.Length))
	if err != nil {
		return fmt.Errorf("download: read block: %w", err)
	}

	if err := conn.SendPiece(index, req.Begin, block); err != nil {
		return fmt.Errorf("download: send piece: %w", err)
	}

	progress.AddUploadedBytes(len(block))
	rates.AddUploaded(addr, len(block))
	return nil
}
