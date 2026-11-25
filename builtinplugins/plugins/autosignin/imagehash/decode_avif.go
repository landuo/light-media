package imagehash

import (
	"bytes"
	"encoding/binary"
	"image"

	"github.com/gen2brain/gav1d/av1"
	avif "light-media/builtinplugins/plugins/autosignin/imagehash/avifport"
)

func decodeAVIF(data []byte) (Raster, error) {
	cfg, err := avif.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Raster{}, err
	}
	if err = checkImageSize(cfg.Width, cfg.Height); err != nil {
		return Raster{}, err
	}
	// Pillow keeps irot/imir as EXIF metadata and requests eight-bit output.
	// The codec's own area limit permits coded planes to differ from ispe.
	img, _, err := avif.DecodeColor(bytes.NewReader(data), avif.Options{
		ConvertPicture: renderAVIFPicture,
	})
	if err != nil {
		return Raster{}, err
	}
	if _, opaque := img.(*image.RGBA); opaque {
		return fromImage(img, "RGB")
	}
	return fromImage(img, "RGBA")
}

func renderAVIFPicture(pic, alpha *av1.Picture, ci avif.ColorInfo, premultiplied bool) (image.Image, error) {
	if pic.BitDepth != 8 && pic.BitDepth != 10 && pic.BitDepth != 12 {
		return nil, avif.ErrUnsupported
	}
	if alpha != nil && (alpha.Layout != av1.LayoutI400 || alpha.BitDepth != pic.BitDepth) {
		return nil, avif.ErrUnsupported
	}
	sx, sy := 1, 1
	switch pic.Layout {
	case av1.LayoutI420:
		sx, sy = 2, 2
	case av1.LayoutI422:
		sx = 2
	case av1.LayoutI400, av1.LayoutI444:
	default:
		return nil, avif.ErrUnsupported
	}
	mono := pic.Layout == av1.LayoutI400
	switch ci.Matrix {
	case 0:
		if !mono && pic.Layout != av1.LayoutI444 {
			return nil, avif.ErrUnsupported
		}
	case 8:
		if !ci.FullRange {
			return nil, avif.ErrUnsupported
		}
	case 16:
		if !ci.FullRange || pic.BitDepth != 10 {
			return nil, avif.ErrUnsupported
		}
	case 3, 10, 11, 13, 14, 17:
		// YCgCoRo requires nine input bits for the source RGBA8 output.
		return nil, avif.ErrUnsupported
	default:
		if ci.Matrix >= 18 {
			return nil, avif.ErrUnsupported
		}
	}

	out := image.NewNRGBA(image.Rect(0, 0, pic.Width, pic.Height))
	if coeff, ok := avifYUVConstants(ci, mono); ok && (!mono || alpha != nil) {
		renderAVIFLibYUV(out, pic, alpha, sx, sy, coeff, premultiplied)
	} else {
		renderAVIFScalar(out, pic, alpha, sx, sy, ci, premultiplied)
	}
	if alpha == nil {
		return &image.RGBA{Pix: out.Pix, Stride: out.Stride, Rect: out.Rect}, nil
	}
	return out, nil
}

// libavif uses native high-depth color conversion for selected alpha paths.
// Other color inputs are downshifted before the byte-plane chroma filter.
func renderAVIFLibYUV(out *image.NRGBA, pic, alpha *av1.Picture, sx, sy int, coeff [6]int, premultiplied bool) {
	shift := pic.BitDepth - 8
	mono := pic.Layout == av1.LayoutI400
	nearest := alpha != nil && pic.BitDepth == 12 && pic.Layout == av1.LayoutI420
	highDepth := alpha != nil && !mono && (pic.BitDepth == 10 || nearest)
	roundedAlpha := mono || nearest
	chroma := func(plane, x, y int) int {
		if nearest {
			return avifSample(pic, plane, x/sx, y/sy) >> shift
		}
		sample := func(cx, cy int) int {
			value := avifSample(pic, plane, cx, cy)
			if !highDepth {
				value >>= shift
			}
			return value
		}
		value := interpolateAVIFChroma(out.Rect, sx, sy, x, y, sample)
		if highDepth {
			value >>= shift
		}
		return value
	}
	maxSample := (1 << pic.BitDepth) - 1
	for y := 0; y < pic.Height; y++ {
		for x := 0; x < pic.Width; x++ {
			luma := avifSample(pic, 0, x, y)
			word := (luma >> shift) * 257
			if highDepth {
				word = (luma << (16 - pic.BitDepth)) | (luma >> (2*pic.BitDepth - 16))
			}
			u, v := 0, 0
			if !mono {
				u, v = chroma(1, x, y)-128, chroma(2, x, y)-128
			}
			r, g, b := avifRGBFromWord(word, u, v, coeff)
			a8 := 255
			if alpha != nil {
				a := avifAlphaSample(alpha, x, y)
				a8 = a >> shift
				if roundedAlpha {
					a8 = int(avifQuantize(float32(a) / float32(maxSample)))
				}
				if premultiplied {
					r, g, b = avifUnattenuate(r, g, b, byte(a8))
				}
			}
			at := out.PixOffset(x, y)
			out.Pix[at], out.Pix[at+1], out.Pix[at+2], out.Pix[at+3] = r, g, b, byte(a8)
		}
	}
}

func avifSample(pic *av1.Picture, plane, x, y int) int {
	at := y * pic.Stride[plane]
	if pic.BitDepth == 8 {
		return int(pic.Data[plane][at+x])
	}
	return int(binary.NativeEndian.Uint16(pic.Data[plane][at+2*x:]))
}

func avifAlphaSample(alpha *av1.Picture, x, y int) int {
	a := avifSample(alpha, 0, x, y)
	if alpha.FullRange {
		return a
	}
	shift := alpha.BitDepth - 8
	maxSample, span := (1<<alpha.BitDepth)-1, 219<<shift
	return (min(span, max(0, a-(16<<shift)))*maxSample + span/2) / span
}

func avifUnattenuate(r, g, b, alpha byte) (byte, byte, byte) {
	if alpha == 255 {
		return r, g, b
	}
	if alpha == 0 {
		return 0, 0, 0
	}
	inverse := min(65535, 65536/int(alpha))
	convert := func(c byte) byte { return byte(min(255, int(c)*257*inverse>>16)) }
	return convert(r), convert(g), convert(b)
}

func avifRGBFromWord(word, u, v int, coeff [6]int) (byte, byte, byte) {
	y := (word * coeff[0] >> 16) + coeff[1]
	clamp := func(value int) byte { return byte(min(255, max(0, value>>6))) }
	return clamp(y + v*coeff[5]), clamp(y - u*coeff[3] - v*coeff[4]), clamp(y + u*coeff[2])
}

// libyuv YuvPixel/MAKEYUVCONSTANTS: fixed-point color conversion follows the
// source Pillow/libavif output. The decoder's float conversion differs by one
// sample and can change the mean threshold even when its AV1 planes are exact.
// Coefficients: YG,YB,UB,UG,VG,VR. Notices are in LIBYUV-*.
func avifYUVConstants(ci avif.ColorInfo, mono bool) ([6]int, bool) {
	matrix := ci.Matrix
	if matrix == 12 {
		switch ci.Primaries {
		case 1, 2:
			matrix = 1
		case 5, 6:
			matrix = 6
		case 9:
			matrix = 9
		}
	}
	if matrix == 0 && mono {
		matrix = 6
	}
	if ci.FullRange {
		switch matrix {
		case 1:
			return [6]int{16320, 32, 119, 12, 30, 101}, true
		case 2, 5, 6:
			return [6]int{16320, 32, 113, 22, 46, 90}, true
		case 9:
			return [6]int{16320, 32, 120, 11, 37, 94}, true
		}
	} else {
		switch matrix {
		case 1:
			return [6]int{18997, -1160, 128, 14, 34, 115}, true
		case 2, 5, 6:
			return [6]int{18997, -1160, 128, 25, 52, 102}, true
		case 9:
			return [6]int{19003, -1160, 128, 12, 42, 107}, true
		}
	}
	return [6]int{}, false
}

func interpolateAVIFChroma(rect image.Rectangle, sx, sy, x, y int, at func(int, int) int) int {
	x0, y0 := x/sx, y/sy
	x1, y1 := x0, y0
	// The filtered libyuv rows copy the final source column at the right
	// edge, including when the destination width is odd.
	if sx == 2 && x < rect.Max.X-1 {
		x1 += x%2*2 - 1
	}
	if sy == 2 {
		y1 += y%2*2 - 1
	}
	x1 = min((rect.Max.X-1)/sx, max(rect.Min.X/sx, x1))
	y1 = min((rect.Max.Y-1)/sy, max(rect.Min.Y/sy, y1))
	if sx == 2 && sy == 2 {
		return (9*at(x0, y0) + 3*at(x1, y0) + 3*at(x0, y1) + at(x1, y1) + 8) >> 4
	}
	if sx == 2 || sy == 2 {
		return (3*at(x0, y0) + at(x1, y1) + 2) >> 2
	}
	return at(x0, y0)
}
