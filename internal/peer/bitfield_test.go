package peer

import "testing"

func TestBitfieldSetAndHasPiece(t *testing.T) {
	const pieceCount = 16
	for setIndex := 0; setIndex < pieceCount; setIndex++ {
		bf := make(Bitfield, (pieceCount+7)/8)
		bf.SetPiece(setIndex)

		for checkIndex := 0; checkIndex < pieceCount; checkIndex++ {
			want := checkIndex == setIndex
			got := bf.HasPiece(checkIndex)
			if got != want {
				t.Errorf("setIndex=%d, checkIndex=%d: got %v, want %v", setIndex, checkIndex, got, want)
			}
		}
	}
}

func TestBitfieldCount(t *testing.T) {
	bf := make(Bitfield, 2) // 16 bits
	bf.SetPiece(0)
	bf.SetPiece(7)
	bf.SetPiece(8)

	if got := bf.Count(); got != 3 {
		t.Fatalf("got %d, want 3", got)
	}
}

func TestBitfieldValidate(t *testing.T) {
	tests := []struct {
		name       string
		bf         Bitfield
		pieceCount int
		wantErr    bool
	}{
		{"exact multiple of 8", make(Bitfield, 2), 16, false},
		{"needs padding byte", make(Bitfield, 2), 15, false},
		{"too short", make(Bitfield, 1), 16, true},
		{"too long", make(Bitfield, 3), 16, true},
	}

	for _, tt := range tests {
		err := Validate(tt.bf, tt.pieceCount)
		if (err != nil) != tt.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", tt.name, err, tt.wantErr)
		}
	}
}
