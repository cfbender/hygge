package openrouter

import "testing"

func TestModels_StableAcrossCalls(t *testing.T) {
	a := Models()
	b := Models()
	if len(a) != len(b) {
		t.Fatalf("len differs: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].Name != b[i].Name {
			t.Errorf("Models()[%d] name: %q vs %q", i, a[i].Name, b[i].Name)
		}
	}
}
