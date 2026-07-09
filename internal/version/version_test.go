package version

import "testing"

func TestCompare(t *testing.T) {
	if Compare("0.1.0", "0.2.0") >= 0 {
		t.Fatal("0.1.0 should be < 0.2.0")
	}
	if Compare("1.0.0", "0.9.9") <= 0 {
		t.Fatal("1.0.0 should be > 0.9.9")
	}
	if Compare("0.1.0", "0.1.0") != 0 {
		t.Fatal("equal versions")
	}
}
