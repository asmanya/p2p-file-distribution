package metainfo

import "testing"

func TestLastPieceLength(t *testing.T) {
	tests := []struct {
		name        string
		totalLength int64
		pieceLength int64
		want        int64
	}{
		{"exact multiple", 100, 50, 50},
		{"non-mulitple", 105, 50, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := &Torrent{TotalLength: tt.totalLength, PieceLength: tt.pieceLength}
			if got := tr.LastPieceLength(); got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestPieceCount(t *testing.T) {
	tr := &Torrent{PiecesHashes: make([][20]byte, 5)}
	if got := tr.PieceCount(); got != 5 {
		t.Errorf("got %d, want 5", got)
	}
}
