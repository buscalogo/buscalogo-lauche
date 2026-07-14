package tray

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"runtime"
)

// encodeTrayIcon serializa o ícone no formato que o systray espera:
// PNG no Linux/macOS; ICO clássico (BMP 32-bit) no Windows.
func encodeTrayIcon(img image.Image) []byte {
	rgba := toRGBA(img)
	if runtime.GOOS == "windows" {
		return encodeICO(rgba)
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, rgba)
	return buf.Bytes()
}

func toRGBA(img image.Image) *image.RGBA {
	if rgba, ok := img.(*image.RGBA); ok {
		return rgba
	}
	b := img.Bounds()
	out := image.NewRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			out.Set(x, y, img.At(x, y))
		}
	}
	return out
}

// encodeICO gera um .ico com uma imagem 32-bit (DIB + máscara AND vazia).
// Formato exigido pelo fyne.io/systray no Windows (LoadImage / IMAGE_ICON).
func encodeICO(img *image.RGBA) []byte {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w <= 0 || h <= 0 {
		return nil
	}
	if w > 256 {
		w = 256
	}
	if h > 256 {
		h = 256
	}

	// XOR bitmap: BGRA bottom-up, 4 bytes/pixel (já alinhado).
	xorSize := w * h * 4
	// AND mask: 1 bit/pixel, cada linha alinhada a 4 bytes, bottom-up.
	andRow := ((w + 31) / 32) * 4
	andSize := andRow * h
	dibSize := 40 + xorSize + andSize

	var b bytes.Buffer
	b.Grow(6 + 16 + dibSize)

	// ICONDIR
	_ = binary.Write(&b, binary.LittleEndian, uint16(0)) // reserved
	_ = binary.Write(&b, binary.LittleEndian, uint16(1)) // type = icon
	_ = binary.Write(&b, binary.LittleEndian, uint16(1)) // count

	// ICONDIRENTRY
	wb, hb := byte(w), byte(h)
	if w == 256 {
		wb = 0
	}
	if h == 256 {
		hb = 0
	}
	b.WriteByte(wb)
	b.WriteByte(hb)
	b.WriteByte(0) // color count
	b.WriteByte(0) // reserved
	_ = binary.Write(&b, binary.LittleEndian, uint16(0)) // planes
	_ = binary.Write(&b, binary.LittleEndian, uint16(32))
	_ = binary.Write(&b, binary.LittleEndian, uint32(dibSize))
	_ = binary.Write(&b, binary.LittleEndian, uint32(22)) // offset após header

	// BITMAPINFOHEADER (height = 2*h: XOR + AND)
	_ = binary.Write(&b, binary.LittleEndian, uint32(40))
	_ = binary.Write(&b, binary.LittleEndian, int32(w))
	_ = binary.Write(&b, binary.LittleEndian, int32(h*2))
	_ = binary.Write(&b, binary.LittleEndian, uint16(1))  // planes
	_ = binary.Write(&b, binary.LittleEndian, uint16(32)) // bit count
	_ = binary.Write(&b, binary.LittleEndian, uint32(0))  // BI_RGB
	_ = binary.Write(&b, binary.LittleEndian, uint32(xorSize+andSize))
	_ = binary.Write(&b, binary.LittleEndian, int32(0)) // x ppm
	_ = binary.Write(&b, binary.LittleEndian, int32(0)) // y ppm
	_ = binary.Write(&b, binary.LittleEndian, uint32(0)) // colors used
	_ = binary.Write(&b, binary.LittleEndian, uint32(0)) // important colors

	// XOR pixels (bottom-up BGRA)
	for y := h - 1; y >= 0; y-- {
		for x := 0; x < w; x++ {
			c := img.RGBAAt(bounds.Min.X+x, bounds.Min.Y+y)
			b.WriteByte(c.B)
			b.WriteByte(c.G)
			b.WriteByte(c.R)
			b.WriteByte(c.A)
		}
	}

	// AND mask (tudo zero = opaco; alpha do XOR manda)
	and := make([]byte, andSize)
	b.Write(and)

	return b.Bytes()
}
