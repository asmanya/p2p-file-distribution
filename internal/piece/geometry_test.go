package piece

import "testing"

func TestLength(t *testing.T) {
	tests := []struct {
		name        string
		index       int
		pieceCount  int
		pieceLength int64
		totalLength int64
		want        int64
		wantErr     bool
	}{
		{"first piece", 0, 46, 32768, 1500000, 32768, false},
		{"middle piece", 20, 46, 32768, 1500000, 32768, false},
		{"last piece, partial (small.torrent)", 45, 46, 32768, 1500000, 25440, false},
		{"last piece, exact multiple (debian iso)", 3019, 3020, 262144, 791674880, 262144, false},
		{"single-piece torrent", 0, 1, 5000, 5000, 5000, false},
		{"index negative", -1, 46, 32768, 1500000, 0, true},
		{"index equals pieceCount", 46, 46, 32768, 1500000, 0, true},
		{"index beyond pieceCount", 100, 46, 32768, 1500000, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Length(tt.index, tt.pieceCount, tt.pieceLength, tt.totalLength)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestRange(t *testing.T) {
	start, end, err := Range(1, 46, 32768, 1500000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if start != 32768 || end != 65536 {
		t.Errorf("got [%d,%d), want [32768, 65536)", start, end)
	}

	// last piece's range must end exactly at totalLength
	_, end, err = Range(45, 46, 32768, 1500000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if end != 1500000 {
		t.Errorf("last piece end = %d, want 1500000 (totalLength)", end)
	}
}

func TestBlockCount(t *testing.T) {
	tests := []struct {
		name        string
		index       int
		pieceCount  int
		pieceLength int64
		totalLength int64
		want        int
	}{
		{"piece smaller than block size", 0, 1, 5000, 5000, 1},
		{"piece exact multiple of block size", 0, 1, 32768, 32768, 2},
		{"piece not a multiple of block size", 0, 1, 20000, 20000, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BlockCount(tt.index, tt.pieceCount, tt.pieceLength, tt.totalLength)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestBlockBounds(t *testing.T) {
	// pieceLength 20000: block 0 = [0, 16384), block 1 = [16384, 20000) (3616 bytes, short)
	offset, length, err := BlockBounds(0, 0, 1, 20000, 20000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if offset != 0 || length != 16384 {
		t.Errorf("block 0: got offset+%d length=%d, want offset=0 length=16384", offset, length)
	}

	offset, length, err = BlockBounds(0, 1, 1, 20000, 20000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if offset != 16384 || length != 3616 {
		t.Errorf("last block: got offset=%d length=%d, want offset=16384 length=3616", offset, length)
	}

	// out-of-range block index must error, not panic
	if _, _, err := BlockBounds(0, 2, 1, 20000, 20000); err == nil {
		t.Fatalf("expected error for out-of-range block index")
	}
}
