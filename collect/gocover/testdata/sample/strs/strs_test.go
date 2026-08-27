package strs

import "testing"

func TestRepeat(t *testing.T) {
	if Repeat("ab", 2) != "ABAB" {
		t.Fatal("repeat")
	}
}
