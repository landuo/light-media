package imagehash

import (
	"encoding/binary"
	"math"
)

// This is the installed Pillow 12.2.0 Resample.c 16bpc path. Unlike its
// 8bpc path it uses unquantized double coefficients and rounds each pass.
// On the source little-endian Linux build the resampler reads both I;16
// and I;16B byte pairs as little-endian; the later converter retains mode.
func gray16(r Raster, shape [2]int) []byte {
	p := append([]byte(nil), r.Pixels...)
	w, h := r.Width, r.Height
	if w != shape[0] {
		cs := coefficients16(w, shape[0])
		out := make([]byte, shape[0]*h*2)
		for y := 0; y < h; y++ {
			for x, k := range cs {
				sum := 0.0
				for i, v := range k.weights {
					sum += float64(binary.LittleEndian.Uint16(p[(y*w+k.start+i)*2:])) * v
				}
				putResampled16(out[(y*shape[0]+x)*2:], sum)
			}
		}
		p = out
		w = shape[0]
	}
	if h != shape[1] {
		cs := coefficients16(h, shape[1])
		out := make([]byte, w*shape[1]*2)
		for y, k := range cs {
			for x := 0; x < w; x++ {
				sum := 0.0
				for i, v := range k.weights {
					sum += float64(binary.LittleEndian.Uint16(p[((k.start+i)*w+x)*2:])) * v
				}
				putResampled16(out[(y*w+x)*2:], sum)
			}
		}
		p = out
	}
	out := make([]byte, shape[0]*shape[1])
	for i := range out {
		var v uint16
		if r.Mode == "I;16B" {
			v = binary.BigEndian.Uint16(p[i*2:])
		} else {
			v = binary.LittleEndian.Uint16(p[i*2:])
		}
		out[i] = byte(min(255, v))
	}
	return out
}

func putResampled16(dst []byte, sum float64) {
	// ROUND_UP is a C int cast after +/-0.5, then CLIP8 on the low remainder
	// and high byte separately. It does not clamp the entire sum to uint16.
	n := int(sum + math.Copysign(0.5, sum))
	dst[0] = clamp(n % 256)
	dst[1] = clamp(n >> 8)
}

type coeff16 struct {
	start   int
	weights []float64
}

func coefficients16(in, out int) []coeff16 {
	scale := float64(float32(in)) / float64(out)
	filterScale := math.Max(scale, 1)
	// Explicit float64 conversions prevent fused multiply-add from changing
	// Pillow's coefficient rounding near an integer-mode half-way result.
	support := float64(2 * filterScale)
	ss := 1 / filterScale
	cs := make([]coeff16, out)
	for x := range cs {
		center := float64((float64(x) + 0.5) * scale)
		start := max(0, int(center-support+0.5))
		end := min(in, int(center+support+0.5))
		ws := make([]float64, end-start)
		total := 0.0
		for i := range ws {
			ws[i] = cubic(float64((float64(i+start) - center + 0.5) * ss))
			total += ws[i]
		}
		if total != 0 {
			for i := range ws {
				ws[i] /= total
			}
		}
		cs[x] = coeff16{start, ws}
	}
	return cs
}
