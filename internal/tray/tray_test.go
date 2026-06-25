package tray

import (
	"bytes"
	"image/png"
	"testing"

	"buscalogo-agent/assets"
)

func TestLogoEmbedded(t *testing.T) {
	b := assets.Logo()
	if len(b) == 0 {
		t.Fatal("logo não embarcada")
	}
	t.Logf("logo bytes: %d", len(b))
}

func TestIconBytes(t *testing.T) {
	for _, running := range []bool{true, false} {
		b := iconBytes(running)
		if len(b) == 0 {
			t.Fatalf("icon running=%v vazio", running)
		}
		img, err := png.Decode(bytes.NewReader(b))
		if err != nil {
			t.Fatalf("icon running=%v não é PNG válido: %v", running, err)
		}
		bounds := img.Bounds()
		if bounds.Dx() != iconSize || bounds.Dy() != iconSize {
			t.Fatalf("icon running=%v tamanho inesperado: %dx%d", running, bounds.Dx(), bounds.Dy())
		}
	}
}
