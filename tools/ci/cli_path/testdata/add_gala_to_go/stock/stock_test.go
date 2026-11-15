package stock

import "testing"

// The module's Go tests keep working, and can call the GALA code of their
// package like any other function of it.
func TestReorder(t *testing.T) {
	items, err := Parse("A-1,12\nB-2,3")
	if err != nil {
		t.Fatal(err)
	}
	got := Reorder(items, 5)
	if len(got) != 1 || got[0] != "B-2" {
		t.Fatalf("Reorder = %v, want [B-2]", got)
	}
}
