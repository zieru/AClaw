package util

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func TestOptimizeImageBytes(t *testing.T) {
	// Create a large synthetic 3000x2000 test image
	w, h := 3000, 2000
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y += 10 {
		for x := 0; x < w; x += 10 {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 100, A: 255})
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("failed to encode synthetic png: %v", err)
	}

	rawBytes := buf.Bytes()
	maxDim := 1024
	optBytes, mime, err := OptimizeImageBytes(rawBytes, maxDim, 80)
	if err != nil {
		t.Fatalf("OptimizeImageBytes failed: %v", err)
	}

	if mime != "image/jpeg" {
		t.Errorf("expected mime image/jpeg, got %s", mime)
	}

	optImg, err := jpeg.Decode(bytes.NewReader(optBytes))
	if err != nil {
		t.Fatalf("failed to decode optimized jpeg: %v", err)
	}

	optBounds := optImg.Bounds()
	if optBounds.Dx() > maxDim || optBounds.Dy() > maxDim {
		t.Errorf("dimensions not within maxDim: %dx%d", optBounds.Dx(), optBounds.Dy())
	}

	expectedH := (2000 * maxDim) / 3000
	if optBounds.Dx() != maxDim || optBounds.Dy() != expectedH {
		t.Errorf("unexpected scaled dimensions: got %dx%d, expected %dx%d", optBounds.Dx(), optBounds.Dy(), maxDim, expectedH)
	}
}

func TestOptimizeBase64Image(t *testing.T) {
	// Create a small 50x50 image
	img := image.NewRGBA(image.Rect(0, 0, 50, 50))
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)

	dataURI, err := OptimizeBase64Image(string(buf.Bytes()), 100, 80)
	// Even with raw bytes passed, OptimizeBase64Image should safely handle it
	if err != nil && len(dataURI) == 0 {
		t.Errorf("expected non-empty return")
	}
}
