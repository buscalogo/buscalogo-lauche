package tray

import (
	"bytes"
	"encoding/binary"
	"image/png"
	"runtime"
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
		if runtime.GOOS == "windows" {
			assertICO(t, b, iconSize)
			continue
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

func TestEncodeICO(t *testing.T) {
	img := generatedIcon(true)
	ico := encodeICO(img)
	assertICO(t, ico, iconSize)
}

func assertICO(t *testing.T, data []byte, size int) {
	t.Helper()
	if len(data) < 22 {
		t.Fatalf("ICO muito curto: %d", len(data))
	}
	if binary.LittleEndian.Uint16(data[0:2]) != 0 {
		t.Fatalf("reserved != 0")
	}
	if binary.LittleEndian.Uint16(data[2:4]) != 1 {
		t.Fatalf("type != icon")
	}
	if binary.LittleEndian.Uint16(data[4:6]) != 1 {
		t.Fatalf("count != 1")
	}
	w, h := int(data[6]), int(data[7])
	if w == 0 {
		w = 256
	}
	if h == 0 {
		h = 256
	}
	if w != size || h != size {
		t.Fatalf("tamanho ICO %dx%d, esperado %d", w, h, size)
	}
	if data[22] != 0x28 {
		t.Fatalf("DIB sem BITMAPINFOHEADER (esperado 0x28), got 0x%02x", data[22])
	}
}
