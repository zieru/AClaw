package util

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"strings"
)

const (
	DefaultMaxDimension = 1568 // Standard optimal dimension for Claude 3.5 & GPT-4o vision
	DefaultJPEGQuality  = 82   // High visual clarity with dramatic file size reduction
	MaxSafeImageBytes   = 600 * 1024 // 600KB threshold below which small images are not recompressed
)

// OptimizeBase64Image takes a base64 string or data URI, resizes if larger than maxDim,
// compresses to JPEG quality, and returns a clean data:image/jpeg;base64,... URI.
func OptimizeBase64Image(dataURI string, maxDim int, quality int) (string, error) {
	if maxDim <= 0 {
		maxDim = DefaultMaxDimension
	}
	if quality <= 0 || quality > 100 {
		quality = DefaultJPEGQuality
	}

	rawBase64 := dataURI
	if strings.HasPrefix(dataURI, "data:") && strings.Contains(dataURI, ";base64,") {
		parts := strings.SplitN(dataURI[5:], ";base64,", 2)
		rawBase64 = parts[1]
	}

	rawBytes, err := base64.StdEncoding.DecodeString(rawBase64)
	if err != nil {
		return dataURI, fmt.Errorf("invalid base64 image data: %w", err)
	}

	optBytes, optMime, err := OptimizeImageBytes(rawBytes, maxDim, quality)
	if err != nil {
		// If decoding fails, fallback to returning original dataURI safely
		return dataURI, nil
	}

	return fmt.Sprintf("data:%s;base64,%s", optMime, base64.StdEncoding.EncodeToString(optBytes)), nil
}

// OptimizeImageBytes inspects image dimensions and file size. If larger than maxDim
// or MaxSafeImageBytes, it scales down proportionally and re-encodes to JPEG.
func OptimizeImageBytes(data []byte, maxDim int, quality int) ([]byte, string, error) {
	if maxDim <= 0 {
		maxDim = DefaultMaxDimension
	}
	if quality <= 0 || quality > 100 {
		quality = DefaultJPEGQuality
	}

	// 1. Fast config check without decoding full pixel buffer
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		// Unsupported or raw format; return as is
		return data, "image/jpeg", nil
	}

	mime := "image/jpeg"
	if format == "png" {
		mime = "image/png"
	}

	// 2. If already small in file size and within max dimensions, return original
	if len(data) <= MaxSafeImageBytes && cfg.Width <= maxDim && cfg.Height <= maxDim {
		return data, mime, nil
	}

	// 3. Decode full image
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return data, mime, err
	}

	origBounds := img.Bounds()
	origW := origBounds.Dx()
	origH := origBounds.Dy()

	targetW, targetH := calculateScaledDimensions(origW, origH, maxDim)

	var finalImg image.Image = img
	if targetW != origW || targetH != origH {
		finalImg = scaleImageNearest(img, targetW, targetH)
	}

	// 4. Encode to JPEG with target quality
	var buf bytes.Buffer
	err = jpeg.Encode(&buf, finalImg, &jpeg.Options{Quality: quality})
	if err != nil {
		return data, mime, err
	}

	return buf.Bytes(), "image/jpeg", nil
}

func calculateScaledDimensions(origW, origH, maxDim int) (int, int) {
	if origW <= maxDim && origH <= maxDim {
		return origW, origH
	}

	if origW >= origH {
		newW := maxDim
		newH := (origH * maxDim) / origW
		if newH < 1 {
			newH = 1
		}
		return newW, newH
	}

	newH := maxDim
	newW := (origW * maxDim) / origH
	if newW < 1 {
		newW = 1
	}
	return newW, newH
}

func scaleImageNearest(src image.Image, targetW, targetH int) image.Image {
	dst := image.NewRGBA(image.Rect(0, 0, targetW, targetH))
	bounds := src.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()

	for y := 0; y < targetH; y++ {
		srcY := bounds.Min.Y + (y*srcH)/targetH
		for x := 0; x < targetW; x++ {
			srcX := bounds.Min.X + (x*srcW)/targetW
			dst.Set(x, y, src.At(srcX, srcY))
		}
	}
	return dst
}
