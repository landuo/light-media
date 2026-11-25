// Package legacycodec reads the Pillow image modes used by the legacy formats.
// The caller supplies the same image-size check as the rest of imagehash.
package legacycodec

import (
	"encoding/binary"
	"errors"
	"image"
	"image/color"
)

type Image struct {
	Mode            string
	Width, Height   int
	Pixels, Palette []byte
}

type SizeCheck func(int, int) error

var errInvalid = errors.New("invalid or truncated legacy image")
var errMode = errors.New("unsupported legacy image mode")

func Decode(b []byte, check SizeCheck) (r Image, recognized bool, err error) {
	switch {
	case len(b) >= 2 && b[0] == 'P' && (b[1] >= '1' && b[1] <= '6' || b[1] == 'f' || b[1] == 'y' || b[1] == '0'):
		r, err = decodePNM(b, check)
	case len(b) >= 2 && b[0] == 10 && (b[1] == 0 || b[1] == 2 || b[1] == 3 || b[1] == 5):
		r, err = decodePCX(b, check)
	case len(b) >= 2 && binary.BigEndian.Uint16(b) == 474:
		r, err = decodeSGI(b, check)
	case len(b) >= 4 && string(b[:4]) == "DDS ":
		r, err = decodeDDS(b, check)
	case len(b) >= 4 && (binary.LittleEndian.Uint32(b) == 12 || binary.LittleEndian.Uint32(b) == 40 || binary.LittleEndian.Uint32(b) == 52 || binary.LittleEndian.Uint32(b) == 56 || binary.LittleEndian.Uint32(b) == 64 || binary.LittleEndian.Uint32(b) == 108 || binary.LittleEndian.Uint32(b) == 124):
		r, err = decodeDIB(b, check, false)
	case len(b) >= 4 && string(b[:4]) == "\x00\x00\x01\x00":
		r, err = decodeICO(b, check)
	case isTGA(b):
		r, err = decodeTGA(b, check)
	default:
		return Image{}, false, nil
	}
	return r, true, err
}

func fromImage(img image.Image, mode string) Image {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	channels := 1
	switch mode {
	case "I;16":
		channels = 2
	case "RGB":
		channels = 3
	case "RGBA":
		channels = 4
	case "LA":
		channels = 2
	}
	r := Image{Mode: mode, Width: w, Height: h, Pixels: make([]byte, w*h*channels)}
	if mode == "P" {
		p := img.(*image.Paletted)
		r.Palette = make([]byte, 768)
		for i, c := range p.Palette {
			if i >= 256 {
				break
			}
			n := color.NRGBAModel.Convert(c).(color.NRGBA)
			copy(r.Palette[i*3:], []byte{n.R, n.G, n.B})
		}
		for y := 0; y < h; y++ {
			copy(r.Pixels[y*w:(y+1)*w], p.Pix[p.PixOffset(bounds.Min.X, bounds.Min.Y+y):][:w])
		}
		return r
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.NRGBAModel.Convert(img.At(bounds.Min.X+x, bounds.Min.Y+y)).(color.NRGBA)
			p := r.Pixels[(y*w+x)*channels:]
			switch mode {
			case "I;16":
				v := color.Gray16Model.Convert(img.At(bounds.Min.X+x, bounds.Min.Y+y)).(color.Gray16).Y
				binary.LittleEndian.PutUint16(p, v)
			case "1", "L":
				p[0] = c.R
			case "LA":
				p[0], p[1] = c.R, c.A
			case "RGB":
				p[0], p[1], p[2] = c.R, c.G, c.B
			case "RGBA":
				p[0], p[1], p[2], p[3] = c.R, c.G, c.B, c.A
			}
		}
	}
	return r
}

func paletteMode(img image.Image, depth int) string {
	p, ok := img.(*image.Paletted)
	if !ok {
		return "RGB"
	}
	gray := len(p.Palette) > 0
	for i, c := range p.Palette {
		n := color.NRGBAModel.Convert(c).(color.NRGBA)
		v := byte(i)
		if depth == 1 {
			v = byte(i * 255)
		}
		if n.R != v || n.G != v || n.B != v {
			gray = false
			break
		}
	}
	if gray {
		if depth == 1 {
			return "1"
		}
		return "L"
	}
	return "P"
}
