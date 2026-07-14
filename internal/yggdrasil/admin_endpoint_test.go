package yggdrasil

import (
	"runtime"
	"strings"
	"testing"
)

func TestAdminListenURI(t *testing.T) {
	uri, err := adminListenURI()
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		if uri != "tcp://127.0.0.1:9901" {
			t.Fatalf("windows AdminListen=%s", uri)
		}
		return
	}
	if !strings.HasPrefix(uri, "unix://") {
		t.Fatalf("unix AdminListen=%s", uri)
	}
	if strings.Contains(uri, "unix://C:") || strings.Contains(uri, `unix://\`) {
		t.Fatalf("path Windows inválido em unix: %s", uri)
	}
}
