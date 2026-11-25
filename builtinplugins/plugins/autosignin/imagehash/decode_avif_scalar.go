package imagehash

import (
	"image"
	"math"

	"github.com/gen2brain/gav1d/av1"
	avif "light-media/builtinplugins/plugins/autosignin/imagehash/avifport"
)

// libavif 1.4.1 reformat.c renders matrices without a libyuv table using
// normalized samples. Its floating-point filter and unpremultiply order
// differ from the byte-plane libyuv path.
func renderAVIFScalar(out *image.NRGBA, pic, alpha *av1.Picture, sx, sy int, ci avif.ColorInfo, premultiplied bool) {
	maxSample := (1 << pic.BitDepth) - 1
	shift := pic.BitDepth - 8
	yBias, ySpan := 0, maxSample
	uvBias, uvSpan := 1<<(pic.BitDepth-1), maxSample
	if !ci.FullRange {
		yBias, ySpan, uvSpan = 16<<shift, 219<<shift, 224<<shift
	}
	if ci.Matrix == 0 {
		uvBias, uvSpan = yBias, ySpan
	}
	yTable, uvTable := make([]float32, maxSample+1), make([]float32, maxSample+1)
	for i := range yTable {
		yTable[i] = float32(i-yBias) / float32(ySpan)
		uvTable[i] = float32(i-uvBias) / float32(uvSpan)
	}
	kr, kg, kb := avifScalarWeights(ci)
	// The built-in fast paths unpremultiply after quantizing RGBA. The slow
	// path instead applies alpha directly to the normalized samples.
	coefficients := ci.Matrix != 0 && ci.Matrix != 8 && ci.Matrix != 16
	postUnattenuate := sx == 1 && sy == 1 &&
		(coefficients || (ci.Matrix == 0 && ci.FullRange && pic.BitDepth == 8 && pic.Layout == av1.LayoutI444))
	chroma := func(plane, x, y int) float32 {
		x0, y0 := x/sx, y/sy
		at := func(cx, cy int) float32 {
			return uvTable[min(maxSample, avifSample(pic, plane, cx, cy))]
		}
		if sx == 1 && sy == 1 {
			return at(x0, y0)
		}
		x1, y1 := x0, y0
		if sx == 2 {
			x1 = min((pic.Width-1)/sx, max(0, x0+x%2*2-1))
		}
		if sy == 2 {
			y1 = min((pic.Height-1)/sy, max(0, y0+y%2*2-1))
		}
		return float32(at(x0, y0)*9/16) + float32(at(x1, y0)*3/16) +
			float32(at(x0, y1)*3/16) + at(x1, y1)/16
	}
	for y := 0; y < pic.Height; y++ {
		for x := 0; x < pic.Width; x++ {
			luma := min(maxSample, avifSample(pic, 0, x, y))
			Y := yTable[luma]
			r, g, b := Y, Y, Y
			if pic.Layout != av1.LayoutI400 {
				u, v := chroma(1, x, y), chroma(2, x, y)
				switch ci.Matrix {
				case 0:
					r, g, b = v, Y, u
				case 8:
					t := Y - u
					r, g, b = t+v, Y+u, t-v
				case 16:
					cg := int(math.Floor(float64(float32(u*float32(maxSample)) + 0.5)))
					co := int(math.Floor(float64(float32(v*float32(maxSample)) + 0.5)))
					t := luma - (cg >> 1)
					blue := min(255, max(0, t-(co>>1)))
					r = float32(min(255, max(0, blue+co))) / 255
					g = float32(min(255, max(0, t+cg))) / 255
					b = float32(blue) / 255
				default:
					r = Y + float32((2*(1-kr))*v)
					b = Y + float32((2*(1-kb))*u)
					g = Y - (2*(float32(kr*(1-kr)*v)+float32(kb*(1-kb)*u)))/kg
				}
			}
			a := float32(1)
			if alpha != nil {
				a = float32(avifAlphaSample(alpha, x, y)) / float32(maxSample)
			}
			if premultiplied && !postUnattenuate {
				if a == 0 {
					r, g, b = 0, 0, 0
				} else if a < 1 {
					r, g, b = r/a, g/a, b/a
				}
			}
			r8, g8, b8, a8 := avifQuantize(r), avifQuantize(g), avifQuantize(b), avifQuantize(a)
			if premultiplied && postUnattenuate {
				r8, g8, b8 = avifUnattenuate(r8, g8, b8, a8)
			}
			at := out.PixOffset(x, y)
			out.Pix[at], out.Pix[at+1], out.Pix[at+2], out.Pix[at+3] = r8, g8, b8, a8
		}
	}
}

func avifQuantize(value float32) byte {
	return byte(0.5 + float32(min(1, max(0, value))*255))
}

func avifScalarWeights(ci avif.ColorInfo) (kr, kg, kb float32) {
	kr, kb = 0.299, 0.114
	switch ci.Matrix {
	case 4:
		kr, kb = 0.30, 0.11
	case 7:
		kr, kb = 0.212, 0.087
	case 12:
		kr, kb = avifDerivedWeights(ci.Primaries)
	}
	return kr, 1 - kr - kb, kb
}

// avifColorPrimariesComputeYCoeffs from libavif 1.4.1 colr.c. Primaries not
// listed by libavif use BT.709 chromaticities.
func avifDerivedWeights(primaries uint16) (float32, float32) {
	p, ok := avifChromaticities[primaries]
	if !ok {
		p = avifChromaticities[1]
	}
	type xyz [3]float32
	point := func(x, y float32) xyz { return xyz{x, y, 1 - float32(x+y)} }
	r, g, b, w := point(p[0], p[1]), point(p[2], p[3]), point(p[4], p[5]), point(p[6], p[7])
	determinant := func(a, b, c xyz) float32 {
		return float32(a[0]*(float32(b[1]*c[2])-float32(c[1]*b[2]))) +
			float32(a[1]*(float32(c[0]*b[2])-float32(b[0]*c[2]))) +
			float32(a[2]*(float32(b[0]*c[1])-float32(c[0]*b[1])))
	}
	scale := w[1] * determinant(r, g, b)
	return (r[1] * determinant(w, g, b)) / scale, (b[1] * determinant(w, r, g)) / scale
}

var avifChromaticities = map[uint16][8]float32{
	1:  {0.64, 0.33, 0.30, 0.60, 0.15, 0.06, 0.3127, 0.329},
	4:  {0.67, 0.33, 0.21, 0.71, 0.14, 0.08, 0.310, 0.316},
	5:  {0.64, 0.33, 0.29, 0.60, 0.15, 0.06, 0.3127, 0.329},
	6:  {0.630, 0.340, 0.310, 0.595, 0.155, 0.070, 0.3127, 0.329},
	7:  {0.630, 0.340, 0.310, 0.595, 0.155, 0.070, 0.3127, 0.329},
	8:  {0.681, 0.319, 0.243, 0.692, 0.145, 0.049, 0.310, 0.316},
	9:  {0.708, 0.292, 0.170, 0.797, 0.131, 0.046, 0.3127, 0.329},
	10: {1, 0, 0, 1, 0, 0, 0.3333, 0.3333},
	11: {0.680, 0.320, 0.265, 0.690, 0.150, 0.060, 0.314, 0.351},
	12: {0.680, 0.320, 0.265, 0.690, 0.150, 0.060, 0.3127, 0.329},
	22: {0.630, 0.340, 0.295, 0.605, 0.155, 0.077, 0.3127, 0.329},
}
