package storage

import (
	"fmt"
	"os"

	"github.com/asmanya/p2p-file-distribution/internal/piece"
)

// File wraps a preallocated output file for positional piece I/O
type File struct {
	f *os.File
}

// Create opens (or creates) the file at path and truncates it to totalLength - the sparse-preallocation this project uses
// (see doc.go / README for the tradeoff).
func Create(path string, totalLength int64) (*File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	if err := f.Truncate(totalLength); err != nil {
		f.Close()
		return nil, err
	}
	return &File{f: f}, nil
}

func (sf *File) Close() error {
	return sf.f.Close()
}

// WritePiece writes data at the byte offset piece index owns within the file - concurrent calls for different indices need
// no lock, since their offset ranges never overlap and positional writes are safe for that.
func (sf *File) WritePiece(index, pieceCount int, pieceLength, totalLength int64, data []byte) error {
	start, _, err := piece.Range(index, pieceCount, pieceLength, totalLength)
	if err != nil {
		return err
	}
	_, err = sf.f.WriteAt(data, start)
	return err
}

// ReadPiece reads piece index's bytes back - used by resume verification and, later, by seeding.
func (sf *File) ReadPiece(index, pieceCount int, pieceLength, totalLength int64) ([]byte, error) {
	start, end, err := piece.Range(index, pieceCount, pieceLength, totalLength)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, end-start)
	if _, err := sf.f.ReadAt(buf, start); err != nil {
		return nil, err
	}
	return buf, nil
}

// Sync flushes buffered writes to disk - called once at download completion, not per piece (fsync is expensive:
// resume's hash verification means a crash before this only costs a re-download of unflushed pieces, not correctness.)
func (sf *File) Sync() error {
	return sf.f.Sync()
}

// ReadBlock reads length bytes starting at begin within piece index - used to serve one block request without reading the whole piece
// just to slice a fraction of it back out.
func (sf *File) ReadBlock(index, pieceCount int, pieceLength, totalLength, begin, length int64) ([]byte, error) {
	start, end, err := piece.Range(index, pieceCount, pieceLength, totalLength)
	if err != nil {
		return nil, err
	}
	if begin < 0 || begin+length > end-start {
		return nil, fmt.Errorf("storage: block [%d, %d) exceeds piece length %d", begin, begin+length, end-start)
	}
	buf := make([]byte, length)
	if _, err := sf.f.ReadAt(buf, start+begin); err != nil {
		return nil, err
	}

	return buf, nil
}
