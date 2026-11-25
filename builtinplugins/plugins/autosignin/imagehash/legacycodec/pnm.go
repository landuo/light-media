package legacycodec

import (
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"strings"
)

type pnmReader struct {
	b   []byte
	pos int
}

func pnmSpace(b byte) bool { return strings.IndexByte(" \t\n\v\f\r", b) >= 0 }
func (p *pnmReader) token() (string, error) {
	var token []byte
	for p.pos < len(p.b) {
		b := p.b[p.pos]
		p.pos++
		if b == '#' {
			for p.pos < len(p.b) && p.b[p.pos] != '\n' && p.b[p.pos] != '\r' {
				p.pos++
			}
			continue
		}
		if pnmSpace(b) {
			if len(token) == 0 {
				continue
			}
			break
		}
		token = append(token, b)
		if len(token) > 10 {
			return "", errInvalid
		}
	}
	if len(token) == 0 {
		return "", errInvalid
	}
	return string(token), nil
}
func (p *pnmReader) number() (int, error) {
	s, e := p.token()
	if e != nil {
		return 0, e
	}
	n, e := strconv.Atoi(s)
	if e != nil {
		return 0, errInvalid
	}
	return n, nil
}
func decodePNM(b []byte, check SizeCheck) (Image, error) {
	p := pnmReader{b: b}
	start := 0
	for p.pos < len(b) && !pnmSpace(b[p.pos]) && p.pos < 6 {
		p.pos++
	}
	magic := string(b[start:p.pos])
	if p.pos < len(b) && pnmSpace(b[p.pos]) {
		p.pos++
	}
	mode := ""
	switch magic {
	case "P1", "P4":
		mode = "1"
	case "P2", "P5":
		mode = "L"
	case "P3", "P6":
		mode = "RGB"
	case "Pf":
		mode = "F"
	case "PyP":
		mode = "P"
	case "P0CMYK", "PyCMYK":
		mode = "CMYK"
	case "PyRGBA":
		mode = "RGBA"
	default:
		return Image{}, errMode
	}
	w, e := p.number()
	if e != nil {
		return Image{}, e
	}
	h, e := p.number()
	if e != nil {
		return Image{}, e
	}
	if e = check(w, h); e != nil {
		return Image{}, e
	}
	c := 1
	switch mode {
	case "RGB":
		c = 3
	case "RGBA", "CMYK":
		c = 4
	}
	maxval := 1
	little := false
	if mode == "F" {
		s, err := p.token()
		if err != nil {
			return Image{}, err
		}
		v, err := strconv.ParseFloat(s, 64)
		if err != nil || v == 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			return Image{}, errInvalid
		}
		little = v < 0
		c = 4
	} else if mode != "1" {
		maxval, e = p.number()
		if e != nil || maxval <= 0 || maxval > 65535 {
			return Image{}, errInvalid
		}
		if maxval > 255 && mode == "L" {
			mode = "I"
			c = 4
		}
	}
	if w > int(^uint(0)>>1)/h/c {
		return Image{}, errInvalid
	}
	r := Image{Mode: mode, Width: w, Height: h, Pixels: make([]byte, w*h*c)}
	if mode == "P" {
		r.Palette = make([]byte, 768)
		for i := 0; i < 256; i++ {
			r.Palette[i*3], r.Palette[i*3+1], r.Palette[i*3+2] = byte(i), byte(i), byte(i)
		}
	}
	if mode == "F" {
		if len(b)-p.pos < len(r.Pixels) {
			return Image{}, errInvalid
		}
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				at := p.pos + (y*w+x)*4
				v := binary.BigEndian.Uint32(b[at:])
				if little {
					v = binary.LittleEndian.Uint32(b[at:])
				}
				binary.LittleEndian.PutUint32(r.Pixels[((h-1-y)*w+x)*4:], v)
			}
		}
		return r, nil
	}
	plain := magic == "P1" || magic == "P2" || magic == "P3"
	if mode == "1" {
		if plain {
			i := 0
			for p.pos < len(b) && i < len(r.Pixels) {
				v := b[p.pos]
				p.pos++
				if v == '#' {
					for p.pos < len(b) && b[p.pos] != '\n' && b[p.pos] != '\r' {
						p.pos++
					}
					continue
				}
				if pnmSpace(v) {
					continue
				}
				if v != '0' && v != '1' {
					return Image{}, errInvalid
				}
				if v == '0' {
					r.Pixels[i] = 255
				}
				i++
			}
			if i != len(r.Pixels) {
				return Image{}, errInvalid
			}
		} else {
			stride := (w + 7) / 8
			if len(b)-p.pos < stride*h {
				return Image{}, errInvalid
			}
			for y := 0; y < h; y++ {
				for x := 0; x < w; x++ {
					if b[p.pos+y*stride+x/8]&(1<<uint(7-x%8)) == 0 {
						r.Pixels[y*w+x] = 255
					}
				}
			}
		}
		return r, nil
	}
	samples := w * h * c
	if mode == "I" {
		samples = w * h
	}
	for i := 0; i < samples; i++ {
		v := 0
		if plain {
			v, e = p.number()
			if e != nil {
				return Image{}, e
			}
		} else {
			n := 1
			if maxval > 255 {
				n = 2
			}
			if p.pos+n > len(b) {
				return Image{}, errInvalid
			}
			v = int(b[p.pos])
			if n == 2 {
				v = int(binary.BigEndian.Uint16(b[p.pos:]))
			}
			p.pos += n
		}
		if v < 0 || (plain && v > maxval) {
			return Image{}, fmt.Errorf("PPM sample exceeds maxval")
		}
		target := 255
		if mode == "I" {
			target = 65535
		}
		// Pillow uses Python round (ties to even) for scaled PPM samples.
		v = min(target, int(math.RoundToEven(float64(v)/float64(maxval)*float64(target))))
		if mode == "I" {
			binary.LittleEndian.PutUint32(r.Pixels[i*4:], uint32(v))
		} else {
			r.Pixels[i] = byte(v)
		}
	}
	return r, nil
}
