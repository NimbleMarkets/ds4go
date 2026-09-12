package ds4

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
)

// ImageInputPNG encodes img as PNG into an ImageInput's Data. PNG is
// lossless, so it suits screenshots, diagrams, and rendered text; for large
// photographs ImageInputJPEG produces far fewer bytes. This is a ds4go
// convenience: libds4 only ingests encoded PNG or JPEG bytes.
func ImageInputPNG(img image.Image) (ImageInput, error) {
	if err := checkImage(img); err != nil {
		return ImageInput{}, err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return ImageInput{}, fmt.Errorf("ds4go: encode PNG: %w", err)
	}
	return ImageInput{Data: buf.Bytes()}, nil
}

// ImageInputJPEG encodes img as JPEG into an ImageInput's Data. quality is
// 1..100; 0 selects the encoder's default (jpeg.DefaultQuality). JPEG drops
// alpha and fine detail, so prefer ImageInputPNG for anything with text.
func ImageInputJPEG(img image.Image, quality int) (ImageInput, error) {
	if err := checkImage(img); err != nil {
		return ImageInput{}, err
	}
	if quality == 0 {
		quality = jpeg.DefaultQuality
	}
	if quality < 1 || quality > 100 {
		return ImageInput{}, fmt.Errorf("ds4go: JPEG quality %d is outside 1..100", quality)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return ImageInput{}, fmt.Errorf("ds4go: encode JPEG: %w", err)
	}
	return ImageInput{Data: buf.Bytes()}, nil
}

func checkImage(img image.Image) error {
	if img == nil {
		return errors.New("ds4go: nil image")
	}
	if img.Bounds().Empty() {
		return errors.New("ds4go: empty image")
	}
	return nil
}
