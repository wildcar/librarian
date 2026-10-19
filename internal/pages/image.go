package pages

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"os"

	// Decoders registered for image.Decode.
	_ "image/gif"
	_ "image/png"

	_ "golang.org/x/image/bmp"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

// ImageTypes are the image originals PrepareImage can read.
var ImageTypes = map[string]bool{
	"image/jpeg": true, "image/png": true, "image/gif": true,
	"image/bmp": true, "image/tiff": true, "image/webp": true,
}

// PrepareImage reads an image original and returns it as a grayscale JPEG with
// the long side at most longSide pixels: the same shape as rendered PDF pages.
func PrepareImage(path string, longSide int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	src, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return nil, fmt.Errorf("decode %s: empty image", path)
	}
	scale := 1.0
	if long := max(w, h); long > longSide {
		scale = float64(longSide) / float64(long)
	}
	dst := image.NewGray(image.Rect(0, 0, max(1, int(float64(w)*scale)), max(1, int(float64(h)*scale))))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 88}); err != nil {
		return nil, fmt.Errorf("encode %s: %w", path, err)
	}
	return buf.Bytes(), nil
}
