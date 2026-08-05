package main

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
)

func resizePNG(src []byte, w int) ([]byte, error) {
	img, err := png.Decode(bytes.NewReader(src))
	if err != nil {
		return nil, fmt.Errorf("failed to decode png: %w", err)
	}

	b := img.Bounds()
	if b.Dx() <= w {
		return src, nil
	}

	h := b.Dy() * w / b.Dx()
	if h < 1 {
		h = 1
	}

	dst := image.NewNRGBA(image.Rect(0, 0, w, h))

	xRatio := float64(b.Dx()) / float64(w)
	yRatio := float64(b.Dy()) / float64(h)

	for y := 0; y < h; y++ {
		sy := (float64(y)+0.5)*yRatio - 0.5
		y0, fy := split(sy, b.Dy())
		y1 := clamp(y0+1, b.Dy())

		for x := 0; x < w; x++ {
			sx := (float64(x)+0.5)*xRatio - 0.5
			x0, fx := split(sx, b.Dx())
			x1 := clamp(x0+1, b.Dx())

			r00, g00, b00, a00 := img.At(b.Min.X+x0, b.Min.Y+y0).RGBA()
			r10, g10, b10, a10 := img.At(b.Min.X+x1, b.Min.Y+y0).RGBA()
			r01, g01, b01, a01 := img.At(b.Min.X+x0, b.Min.Y+y1).RGBA()
			r11, g11, b11, a11 := img.At(b.Min.X+x1, b.Min.Y+y1).RGBA()

			pr := bilinear(r00, r10, r01, r11, fx, fy)
			pg := bilinear(g00, g10, g01, g11, fx, fy)
			pb := bilinear(b00, b10, b01, b11, fx, fy)
			pa := bilinear(a00, a10, a01, a11, fx, fy)

			i := dst.PixOffset(x, y)
			dst.Pix[i+0] = unpremultiply(pr, pa)
			dst.Pix[i+1] = unpremultiply(pg, pa)
			dst.Pix[i+2] = unpremultiply(pb, pa)
			dst.Pix[i+3] = uint8(pa / 257)
		}
	}

	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&buf, dst); err != nil {
		return nil, fmt.Errorf("failed to encode png: %w", err)
	}
	return buf.Bytes(), nil
}

func split(v float64, max int) (int, float64) {
	if v < 0 {
		return 0, 0
	}
	i := int(v)
	if i >= max-1 {
		return max - 1, 0
	}
	return i, v - float64(i)
}

func clamp(v, max int) int {
	if v >= max {
		return max - 1
	}
	return v
}

func bilinear(v00, v10, v01, v11 uint32, fx, fy float64) float64 {
	top := float64(v00)*(1-fx) + float64(v10)*fx
	bottom := float64(v01)*(1-fx) + float64(v11)*fx
	return top*(1-fy) + bottom*fy
}

func unpremultiply(v, a float64) uint8 {
	if a <= 0 {
		return 0
	}
	c := v / a * 255
	if c > 255 {
		c = 255
	}
	return uint8(c + 0.5)
}
