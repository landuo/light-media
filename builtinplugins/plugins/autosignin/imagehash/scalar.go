package imagehash

import (
	"encoding/binary"
	"math"
)

// Pillow 12.2.0 Resample.c uses double coefficients for both 32-bit modes.
// Each pass writes the original mode: ROUND_UP to int32 for I, float32 for F.
// Convert.c's i2l/f2l clamp and truncate only after both resize passes.
func grayScalar(r Raster, shape [2]int) []byte {
	p := r.Pixels
	if r.Height > r.Width*100 && shape[1] < r.Height {
		// Image.resize's tall-image path deliberately reverses the pass order.
		p = resizeScalar(p, r.Width, r.Height, r.Width, shape[1], r.Mode)
		p = resizeScalar(p, r.Width, shape[1], shape[0], shape[1], r.Mode)
	} else {
		p = resizeScalar(p, r.Width, r.Height, shape[0], shape[1], r.Mode)
	}
	out := make([]byte, shape[0]*shape[1])
	for i := range out {
		v := scalarValue(p[i*4:], r.Mode)
		if math.IsNaN(v) || v <= 0 {
			out[i] = 0
		} else if v >= 255 {
			out[i] = 255
		} else {
			out[i] = byte(v)
		}
	}
	return out
}

func resizeScalar(p []byte, w, h, ow, oh int, mode string) []byte {
	if w != ow {
		cs := coefficients16(w, ow)
		out := make([]byte, ow*h*4)
		for y := 0; y < h; y++ {
			for x, k := range cs {
				sum := 0.0
				for i, v := range k.weights {
					sum += float64(scalarValue(p[(y*w+k.start+i)*4:], mode) * v)
				}
				putScalar(out[(y*ow+x)*4:], sum, mode)
			}
		}
		p, w = out, ow
	}
	if h != oh {
		cs := coefficients16(h, oh)
		out := make([]byte, w*oh*4)
		for y, k := range cs {
			for x := 0; x < w; x++ {
				sum := 0.0
				for i, v := range k.weights {
					sum += float64(scalarValue(p[((k.start+i)*w+x)*4:], mode) * v)
				}
				putScalar(out[(y*w+x)*4:], sum, mode)
			}
		}
		p = out
	}
	return p
}

func scalarValue(p []byte, mode string) float64 {
	v := binary.LittleEndian.Uint32(p)
	if mode == "I" {
		return float64(int32(v))
	}
	return float64(math.Float32frombits(v))
}

func putScalar(p []byte, sum float64, mode string) {
	if mode == "I" {
		rounded := sum + math.Copysign(0.5, sum)
		// The fixed Linux x86_64 Pillow build's C cast returns INT_MIN on
		// overflow. Do not inherit an ARM64 cast's saturating behavior.
		n := int32(math.MinInt32)
		if rounded >= math.MinInt32 && rounded < float64(math.MaxInt32)+1 {
			n = int32(rounded)
		}
		binary.LittleEndian.PutUint32(p, uint32(n))
	} else {
		binary.LittleEndian.PutUint32(p, math.Float32bits(float32(sum)))
	}
}
