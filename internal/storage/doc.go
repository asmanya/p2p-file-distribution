// Package storage handles on-disk I/O for verified pieces: preallocating the output file, writing each piece to its correct
// offset, and reading existing data back to verify it on resume. It does not decide which piece to download next - that policy
// belongs to the download package, not here.
package storage
