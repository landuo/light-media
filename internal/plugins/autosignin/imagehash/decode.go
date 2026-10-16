package imagehash

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/gif"
	"image/png"
)

// Hash decodes formats whose pixel/mode mapping has an exact reference suite.
// JPEG uses the installed Pillow-compatible path, never Go's different IDCT.
func Hash(encoded []byte) (string, error) {
	r, e := Decode(encoded)
	if e != nil {
		return "", e
	}
	return Raw(r, [2]int{10, 10})
}

func Decode(encoded []byte) (Raster, error) {
	if len(encoded) == 0 || len(encoded) > 8<<20 {
		return Raster{}, ErrInvalid
	}
	if len(encoded) >= 3 && encoded[0] == 0xff && encoded[1] == 0xd8 && encoded[2] == 0xff {
		return DecodeJPEG(encoded)
	}
	var cfg image.Config
	var img image.Image
	var mode string
	var gifPalette []byte
	var gifFill byte
	var err error
	if len(encoded) >= 26 && string(encoded[:8]) == "\x89PNG\r\n\x1a\n" {
		if encoded[24] > 8 {
			return Raster{}, ErrUnsupported
		}
		switch encoded[25] {
		case 0:
			mode = "L"
			if encoded[24] == 1 {
				mode = "1"
			}
		case 2:
			mode = "RGB"
		case 3:
			mode = "P"
		case 4:
			mode = "LA"
		case 6:
			mode = "RGBA"
		default:
			return Raster{}, ErrUnsupported
		}
		cfg, err = png.DecodeConfig(bytes.NewReader(encoded))
		if err == nil && bounded(cfg) {
			img, err = png.Decode(bytes.NewReader(encoded))
		} else if err == nil {
			err = ErrInvalid
		}
	} else if len(encoded) >= 6 && (string(encoded[:6]) == "GIF87a" || string(encoded[:6]) == "GIF89a") {
		gifPalette, gifFill, err = firstGIFPalette(encoded)
		if err != nil {
			return Raster{}, err
		}
		mode = "L"
		for i := 0; i < len(gifPalette); i += 3 {
			if gifPalette[i] != byte(i/3) || gifPalette[i+1] != byte(i/3) || gifPalette[i+2] != byte(i/3) {
				mode = "P"
				break
			}
		}
		cfg, err = gif.DecodeConfig(bytes.NewReader(encoded))
		if err == nil && bounded(cfg) {
			img, err = gif.Decode(bytes.NewReader(encoded))
		} else if err == nil {
			err = ErrInvalid
		}
	} else {
		return Raster{}, ErrUnsupported
	}
	if err != nil {
		return Raster{}, err
	}
	if gifPalette != nil {
		p, ok := img.(*image.Paletted)
		if !ok {
			return Raster{}, ErrUnsupported
		}
		r := Raster{Mode: mode, Width: cfg.Width, Height: cfg.Height, Pixels: bytes.Repeat([]byte{gifFill}, cfg.Width*cfg.Height)}
		if mode == "P" {
			r.Palette = make([]byte, 768)
			copy(r.Palette, gifPalette)
		}
		b := p.Bounds()
		for y := b.Min.Y; y < b.Max.Y; y++ {
			copy(r.Pixels[y*cfg.Width+b.Min.X:y*cfg.Width+b.Max.X], p.Pix[p.PixOffset(b.Min.X, y):][:b.Dx()])
		}
		return r, nil
	}
	return fromImage(img, mode)
}

// Read the first frame's original palette rather than Go's alpha-converted
// color.Palette: a transparent entry still contributes RGB to Pillow's L.
func firstGIFPalette(b []byte) ([]byte, byte, error) {
	if len(b) < 13 {
		return nil, 0, ErrInvalid
	}
	i := 13
	var pal []byte
	readPalette := func(flags byte) bool {
		n := 3 * (2 << uint(flags&7))
		if i+n > len(b) {
			return false
		}
		pal = b[i : i+n]
		i += n
		return true
	}
	if b[10]&128 != 0 && !readPalette(b[10]) {
		return nil, 0, ErrInvalid
	}
	var fill byte
	for i < len(b) {
		switch b[i] {
		case '!':
			if i+2 > len(b) {
				return nil, 0, ErrInvalid
			}
			label := b[i+1]
			i += 2
			for {
				if i >= len(b) {
					return nil, 0, ErrInvalid
				}
				n := int(b[i])
				i++
				if n == 0 {
					break
				}
				if i+n > len(b) {
					return nil, 0, ErrInvalid
				}
				if label == 0xf9 && n == 4 && b[i]&1 != 0 {
					fill = b[i+3]
				}
				i += n
			}
		case ',':
			if i+10 > len(b) {
				return nil, 0, ErrInvalid
			}
			flags := b[i+9]
			w, h := int(binary.LittleEndian.Uint16(b[i+5:i+7])), int(binary.LittleEndian.Uint16(b[i+7:i+9]))
			if w < 1 || h < 1 {
				return nil, 0, ErrInvalid
			}
			i += 10
			if flags&128 != 0 && !readPalette(flags) {
				return nil, 0, ErrInvalid
			}
			if pal == nil {
				return nil, 0, ErrUnsupported
			}
			return pal, fill, nil
		default:
			return nil, 0, ErrInvalid
		}
	}
	return nil, 0, ErrInvalid
}

func bounded(c image.Config) bool {
	return c.Width > 0 && c.Height > 0 && c.Width <= 8192 && c.Height <= 8192 && int64(c.Width)*int64(c.Height) <= 16<<20
}

func fromImage(img image.Image, mode string) (Raster, error) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	r := Raster{Mode: mode, Width: w, Height: h, Pixels: make([]byte, w*h*channels(mode))}
	if mode == "P" {
		p, ok := img.(*image.Paletted)
		if !ok {
			return Raster{}, ErrUnsupported
		}
		r.Palette = make([]byte, 768)
		for i, c := range p.Palette {
			n := color.NRGBAModel.Convert(c).(color.NRGBA)
			r.Palette[i*3] = n.R
			r.Palette[i*3+1] = n.G
			r.Palette[i*3+2] = n.B
		}
		for y := 0; y < h; y++ {
			copy(r.Pixels[y*w:(y+1)*w], p.Pix[p.PixOffset(b.Min.X, b.Min.Y+y):][:w])
		}
		return r, nil
	}
	c := channels(mode)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			n := color.NRGBAModel.Convert(img.At(x+b.Min.X, y+b.Min.Y)).(color.NRGBA)
			p := r.Pixels[(y*w+x)*c:]
			switch mode {
			case "1", "L":
				p[0] = n.R
			case "LA":
				p[0] = n.R
				p[1] = n.A
			case "RGB":
				p[0] = n.R
				p[1] = n.G
				p[2] = n.B
			case "RGBA":
				p[0] = n.R
				p[1] = n.G
				p[2] = n.B
				p[3] = n.A
			}
		}
	}
	return r, nil
}
