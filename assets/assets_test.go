package assets

import (
	"runtime"
	"testing"
)

func TestPlatformFileName(t *testing.T) {
	if runtime.GOOS != "windows" {
		if platformFileName("yggdrasil") != "yggdrasil" {
			t.Fatal(platformFileName("yggdrasil"))
		}
		return
	}
	if platformFileName("yggdrasil") != "yggdrasil.exe" {
		t.Fatalf("got %s", platformFileName("yggdrasil"))
	}
	if platformFileName("wintun.dll") != "wintun.dll" {
		t.Fatalf("dll munged: %s", platformFileName("wintun.dll"))
	}
	if platformFileName("coredns.exe") != "coredns.exe" {
		t.Fatalf("exe remunged: %s", platformFileName("coredns.exe"))
	}
}
