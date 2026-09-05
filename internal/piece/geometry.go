package piece

import "fmt"

const BlockSize = 16 * 1024

// Length returns the actual length of piece index, given the torrent's total length and standard piece length. Every
// piece except the last is exactly pieceLength, the last piece is shorter unless totalLength divides evenly by pieceLength,
// in which case it's a full piece too.
func Length(index, pieceCount int, pieceLength, totalLength int64) (int64, error) {
	if index < 0 || index >= pieceCount {
		return 0, fmt.Errorf("piece: index %d out of range [0,%d)", index, pieceCount)
	}
	if index < pieceCount-1 {
		return pieceLength, nil
	}
	remainder := totalLength % pieceLength
	if remainder == 0 {
		return pieceLength, nil
	}
	return remainder, nil
}

// Range returns the byte offsets [start, end) of piece index within the whole torrent's concatenated data.
func Range(index, pieceCount int, pieceLength, totalLength int64) (start, end int64, err error) {
	length, err := Length(index, pieceCount, pieceLength, totalLength)
	if err != nil {
		return 0, 0, err
	}
	start = int64(index) * pieceLength
	return start, start + length, nil
}

// BlockCount returns how many BlockSize blocks piece index is split into.
func BlockCount(index, pieceCount int, pieceLength, totalLength int64) (int, error) {
	length, err := Length(index, pieceCount, pieceLength, totalLength)
	if err != nil {
		return 0, err
	}
	return int(length+BlockSize-1) / BlockSize, nil
}

// BlockBounds returns the offset and length of blockIndex within piece index. Every block is BlockSize except the last
// one in a piece, which may be shorter
func BlockBounds(index, blockIndex, pieceCount int, pieceLength, totalLength int64) (offset, length int64, err error) {
	pieceLen, err := Length(index, pieceCount, pieceLength, totalLength)
	if err != nil {
		return 0, 0, err
	}
	blockCount, err := BlockCount(index, pieceCount, pieceLength, totalLength)
	if err != nil {
		return 0, 0, err
	}
	if blockIndex < 0 || blockIndex >= blockCount {
		return 0, 0, fmt.Errorf("piece: block index %d is out of range[0,%d)", blockIndex, blockCount)
	}

	offset = int64(blockIndex) * BlockSize
	if blockIndex == blockCount-1 {
		return offset, pieceLen - offset, nil
	}
	return offset, BlockSize, nil
}
