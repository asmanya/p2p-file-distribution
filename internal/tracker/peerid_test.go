package tracker

import "testing"

func TestGeneratePeerID(t *testing.T) {
	id, err := GeneratePeerID()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(id) != 20 {
		t.Fatalf("expected length 20, got %d", len(id))
	}
	if string(id[:8]) != "-GO0001-" {
		t.Fatalf("expected prefix -GO0001-, got %q", id[:8])
	}
}

func TestGeneratePeerIDUnique(t *testing.T) {
	id1, _ := GeneratePeerID()
	id2, _ := GeneratePeerID()
	if id1 == id2 {
		t.Fatalf("two calls produced identical peer IDs")
	}
}
