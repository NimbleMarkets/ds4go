package ds4

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func testImage() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 32), G: uint8(y * 32), B: 128, A: 255})
		}
	}
	return img
}

func TestImageInputPNGEncodesAPNG(t *testing.T) {
	in, err := ImageInputPNG(testImage())
	if err != nil {
		t.Fatalf("ImageInputPNG: %v", err)
	}
	if in.Path != "" || !bytes.HasPrefix(in.Data, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("ImageInputPNG = {Path:%q, Data[:8]:%q}, want PNG bytes and no path", in.Path, in.Data[:min(8, len(in.Data))])
	}
	decoded, err := png.Decode(bytes.NewReader(in.Data))
	if err != nil {
		t.Fatalf("png.Decode: %v", err)
	}
	if got := decoded.Bounds(); got != image.Rect(0, 0, 8, 8) {
		t.Errorf("decoded bounds = %v, want 8x8", got)
	}
}

func TestImageInputJPEGEncodesAJPEG(t *testing.T) {
	in, err := ImageInputJPEG(testImage(), 90)
	if err != nil {
		t.Fatalf("ImageInputJPEG: %v", err)
	}
	if in.Path != "" || !bytes.HasPrefix(in.Data, []byte{0xff, 0xd8, 0xff}) {
		t.Fatalf("ImageInputJPEG = {Path:%q, Data[:3]:%x}, want JPEG bytes and no path", in.Path, in.Data[:min(3, len(in.Data))])
	}
	if _, err := jpeg.Decode(bytes.NewReader(in.Data)); err != nil {
		t.Fatalf("jpeg.Decode: %v", err)
	}
	// Zero picks the encoder's default quality; anything else outside 1..100
	// is a caller error rather than a silent clamp.
	if _, err := ImageInputJPEG(testImage(), 0); err != nil {
		t.Errorf("quality 0 (default) rejected: %v", err)
	}
	for _, q := range []int{-1, 101} {
		if _, err := ImageInputJPEG(testImage(), q); err == nil {
			t.Errorf("quality %d accepted", q)
		}
	}
}

func TestImageInputHelpersRejectNilAndEmptyImages(t *testing.T) {
	if _, err := ImageInputPNG(nil); err == nil {
		t.Error("ImageInputPNG(nil) succeeded")
	}
	if _, err := ImageInputJPEG(nil, 0); err == nil {
		t.Error("ImageInputJPEG(nil) succeeded")
	}
	empty := image.NewRGBA(image.Rect(0, 0, 0, 0))
	if _, err := ImageInputPNG(empty); err == nil {
		t.Error("ImageInputPNG(empty) succeeded")
	}
	if _, err := ImageInputJPEG(empty, 0); err == nil {
		t.Error("ImageInputJPEG(empty) succeeded")
	}
}

func TestImageInputHelpersFeedTheEncoder(t *testing.T) {
	eng, _ := visionMockEngine(t)
	enc := NewImageEncoder(eng)
	in, err := ImageInputPNG(testImage())
	if err != nil {
		t.Fatal(err)
	}
	emb, err := enc.Encode(in)
	if err != nil {
		t.Fatalf("Encode(PNG input): %v", err)
	}
	emb.Free()
	if !enc.cached(in.Data) {
		t.Error("PNG input was not cached by its bytes")
	}
}
