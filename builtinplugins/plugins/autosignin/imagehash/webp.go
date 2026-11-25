package imagehash

import "image"

// libwebp 1.6.0 src/dsp/{upsampling.c,yuv.h} fancy upsampling and limited-range
// YUV conversion. x/image provides the pure VP8 entropy/transform decoder;
// image/color's full-range YCbCr conversion is not used for WebP pixels.
// Copyright 2010-2011 Google Inc. See LIBWEBP-COPYING and LIBWEBP-PATENTS.
func webpRaster(img image.Image) (Raster, bool) {
	var yuv *image.YCbCr
	var alpha *image.NYCbCrA
	switch m := img.(type) {
	case *image.YCbCr:
		yuv = m
	case *image.NYCbCrA:
		yuv = &m.YCbCr
		alpha = m
	default:
		return Raster{}, false
	}
	w, h := yuv.Rect.Dx(), yuv.Rect.Dy()
	c := 3
	mode := "RGB"
	if alpha != nil {
		c = 4
		mode = "RGBA"
	}
	r := Raster{Mode: mode, Width: w, Height: h, Pixels: make([]byte, w*h*c)}
	cw, ch := (w+1)/2, (h+1)/2
	sample := func(plane []byte, x, y int) int {
		return int(plane[min(ch-1, max(0, y))*yuv.CStride+min(cw-1, max(0, x))])
	}
	for y := 0; y < h; y++ {
		top, cur, isTop := 0, 0, true
		if y > 0 {
			if y%2 == 1 {
				top = (y - 1) / 2
				cur = min(top+1, ch-1)
			} else {
				top = y/2 - 1
				cur = y / 2
				isTop = false
			}
		}
		for x := 0; x < w; x++ {
			interpolate := func(plane []byte) int {
				if x == 0 || x == w-1 && w%2 == 0 {
					cx := 0
					if x != 0 {
						cx = cw - 1
					}
					a, b := sample(plane, cx, top), sample(plane, cx, cur)
					if isTop {
						return (3*a + b + 2) >> 2
					}
					return (a + 3*b + 2) >> 2
				}
				right := (x + 1) / 2
				left := right - 1
				tl, t, l, v := sample(plane, left, top), sample(plane, right, top), sample(plane, left, cur), sample(plane, right, cur)
				avg := tl + t + l + v + 8
				diag12 := (avg + 2*(t+l)) >> 3
				diag03 := (avg + 2*(tl+v)) >> 3
				if isTop {
					if x%2 == 1 {
						return (diag12 + tl) >> 1
					}
					return (diag03 + t) >> 1
				}
				if x%2 == 1 {
					return (diag03 + l) >> 1
				}
				return (diag12 + v) >> 1
			}
			yy := int(yuv.Y[y*yuv.YStride+x])
			u, v := interpolate(yuv.Cb), interpolate(yuv.Cr)
			p := r.Pixels[(y*w+x)*c:]
			p[0] = clamp(((yy*19077)>>8 + (v*26149)>>8 - 14234) >> 6)
			p[1] = clamp(((yy*19077)>>8 - (u*6419)>>8 - (v*13320)>>8 + 8708) >> 6)
			p[2] = clamp(((yy*19077)>>8 + (u*33050)>>8 - 17685) >> 6)
			if alpha != nil {
				p[3] = alpha.A[y*alpha.AStride+x]
			}
		}
	}
	return r, true
}
