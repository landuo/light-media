// Package imagehash implements the installed Tjupt handler's resize/gray/hash
// pipeline. This is a Go translation of Pillow 12.2.0's relevant pixel paths;
// see reference/Pillow-LICENSE for the retained upstream copyright and license.
package imagehash

import (
	"errors"
	"math"
)

var ErrUnsupported = errors.New("image mode or encoding has not been verified")
var ErrInvalid = errors.New("invalid image or hash dimensions")

// Raster uses tightly packed Pillow mode bytes. Mode 1 is unpacked 0/255, and
// mode P has one index byte per pixel and a packed 256-entry RGB palette.
// Modes I and F use little-endian int32 and float32 pixels, respectively.
type Raster struct {
	Mode            string
	Width, Height   int
	Pixels, Palette []byte
}

func channels(mode string) int {
	switch mode {
	case "1", "P", "L":
		return 1
	case "LA", "I;16", "I;16B":
		return 2
	case "RGB":
		return 3
	case "RGBA", "RGBX", "CMYK", "I", "F":
		return 4
	}
	return 0
}

// Raw hashes decoded bytes without guessing how another decoder maps a format
// to a Pillow mode. All dimensions are bounded before allocation.
func Raw(r Raster, shape [2]int) (string, error) {
	gray, err := Gray(r, shape)
	if err != nil {
		return "", err
	}
	var sum int64
	for _, x := range gray {
		sum += int64(x)
	}
	avg := float64(sum) / float64(len(gray))
	out := make([]byte, len(gray))
	for i, x := range gray {
		out[i] = '0'
		if float64(x) > avg {
			out[i] = '1'
		}
	}
	return string(out), nil
}

// Compare preserves the source's denominator of 100 even for equal length
// nonbinary strings. The original iterates Python characters, not UTF-8 bytes.
func Compare(a, b string) float64 { v, _ := CompareShape(a, b, [2]int{10, 10}); return v }

func CompareShape(a, b string, shape [2]int) (float64, error) {
	x, y := []rune(a), []rune(b)
	if len(x) != len(y) {
		return -1, nil
	}
	if shape[0] == 0 || shape[1] == 0 {
		return 0, ErrInvalid
	}
	n := 0
	for i, c := range x {
		if c == y[i] {
			n++
		}
	}
	return float64(n) / (float64(shape[0]) * float64(shape[1])), nil
}

// Gray exposes the intermediate pixels for exact offline reference checks.
func Gray(r Raster, shape [2]int) ([]byte, error) {
	c := channels(r.Mode)
	if c == 0 {
		return nil, ErrUnsupported
	}
	if r.Width < 1 || r.Height < 1 || shape[0] < 1 || shape[1] < 1 ||
		r.Width > int(^uint(0)>>1)/r.Height/c || shape[0] > int(^uint(0)>>1)/shape[1] || len(r.Pixels) != r.Width*r.Height*c {
		return nil, ErrInvalid
	}
	if r.Mode == "P" && len(r.Palette) != 768 {
		return nil, ErrInvalid
	}
	if r.Mode == "I;16" || r.Mode == "I;16B" {
		return gray16(r, shape), nil
	}
	if r.Mode == "I" || r.Mode == "F" {
		return grayScalar(r, shape), nil
	}
	pix := append([]byte(nil), r.Pixels...)
	if r.Width != shape[0] || r.Height != shape[1] {
		if r.Mode == "P" || r.Mode == "1" {
			pix = nearest(pix, r.Width, r.Height, shape[0], shape[1])
		} else {
			alpha := r.Mode == "LA" || r.Mode == "RGBA"
			if alpha {
				for i := 0; i < len(pix); i += c {
					for ch := 0; ch < c-1; ch++ {
						pix[i+ch] = mul255(pix[i+ch], pix[i+c-1])
					}
				}
			}
			if r.Height > r.Width*100 && shape[1] < r.Height {
				pix = resize(pix, r.Width, r.Height, r.Width, shape[1], c)
				pix = resize(pix, r.Width, shape[1], shape[0], shape[1], c)
			} else {
				pix = resize(pix, r.Width, r.Height, shape[0], shape[1], c)
			}
			if alpha {
				for i := 0; i < len(pix); i += c {
					a := int(pix[i+c-1])
					if a != 0 && a != 255 {
						for ch := 0; ch < c-1; ch++ {
							pix[i+ch] = clamp(int(pix[i+ch]) * 255 / a)
						}
					}
				}
			}
		}
	}
	out := make([]byte, shape[0]*shape[1])
	for i := range out {
		p := pix[i*c : (i+1)*c]
		switch r.Mode {
		case "L", "LA":
			out[i] = p[0]
		case "1":
			if p[0] != 0 {
				out[i] = 255
			}
		case "P":
			off := int(p[0]) * 3
			out[i] = luma(r.Palette[off : off+3])
		case "CMYK":
			nk := byte(255 - p[3])
			var rgb [3]byte
			for ch := 0; ch < 3; ch++ {
				rgb[ch] = nk - mul255(p[ch], nk)
			}
			out[i] = luma(rgb[:])
		default:
			out[i] = luma(p)
		}
	}
	return out, nil
}

func luma(p []byte) byte {
	return byte((int(p[0])*19595 + int(p[1])*38470 + int(p[2])*7471 + 32768) >> 16)
}
func mul255(a, b byte) byte { t := int(a)*int(b) + 128; return byte(((t >> 8) + t) >> 8) }
func clamp(n int) byte {
	if n < 0 {
		return 0
	}
	if n > 255 {
		return 255
	}
	return byte(n)
}

// Repeated addition deliberately matches ImagingScaleAffine's coordinate path.
func nearest(p []byte, w, h, ow, oh int) []byte {
	out := make([]byte, ow*oh)
	xs := make([]int, ow)
	sx, sy := float64(w)/float64(ow), float64(h)/float64(oh)
	x := sx * 0.5
	for i := range xs {
		xs[i] = int(x)
		x += sx
	}
	y := sy * 0.5
	for row := 0; row < oh; row++ {
		for col := 0; col < ow; col++ {
			out[row*ow+col] = p[int(y)*w+xs[col]]
		}
		y += sy
	}
	return out
}

type coeff struct {
	start   int
	weights []int32
}

func cubic(x float64) float64 {
	x = math.Abs(x)
	// Keep Pillow's separate double operations even on architectures with FMA.
	if x < 1 {
		p := float64(1.5*x) - 2.5
		p = float64(p * x)
		return float64(p*x) + 1
	}
	if x < 2 {
		p := float64((x-5)*x) + 8
		p = float64(p*x) - 4
		return p * -0.5
	}
	return 0
}

func coefficients(in, out int) []coeff {
	scale := float64(float32(in)) / float64(out)
	filterScale := math.Max(scale, 1)
	support := 2 * filterScale
	ss := 1 / filterScale
	cs := make([]coeff, out)
	for xx := range cs {
		center := (float64(xx) + 0.5) * scale
		start := max(0, int(center-support+0.5))
		end := min(in, int(center+support+0.5))
		ws := make([]float64, end-start)
		total := 0.0
		for x := range ws {
			ws[x] = cubic((float64(x+start) - center + 0.5) * ss)
			total += ws[x]
		}
		cs[xx] = coeff{start: start, weights: make([]int32, len(ws))}
		for x, v := range ws {
			if total != 0 {
				v /= total
			}
			if v < 0 {
				cs[xx].weights[x] = int32(v*(1<<22) - 0.5)
			} else {
				cs[xx].weights[x] = int32(v*(1<<22) + 0.5)
			}
		}
	}
	return cs
}

func resize(p []byte, w, h, ow, oh, c int) []byte {
	if w != ow {
		cs := coefficients(w, ow)
		o := make([]byte, ow*h*c)
		for y := 0; y < h; y++ {
			for x, k := range cs {
				for ch := 0; ch < c; ch++ {
					var s int32 = 1 << 21
					for i, v := range k.weights {
						s += int32(p[(y*w+k.start+i)*c+ch]) * v
					}
					o[(y*ow+x)*c+ch] = clamp(int(s >> 22))
				}
			}
		}
		p = o
		w = ow
	}
	if h != oh {
		cs := coefficients(h, oh)
		o := make([]byte, w*oh*c)
		for y, k := range cs {
			for x := 0; x < w; x++ {
				for ch := 0; ch < c; ch++ {
					var s int32 = 1 << 21
					for i, v := range k.weights {
						s += int32(p[((k.start+i)*w+x)*c+ch]) * v
					}
					o[(y*w+x)*c+ch] = clamp(int(s >> 22))
				}
			}
		}
		p = o
	}
	return p
}
