package legacycodec

import (
	"encoding/binary"
	"errors"
)

func isTGA(b []byte) bool {
	if len(b) < 18 || b[1] > 1 {
		return false
	}
	switch b[2] {
	case 1, 2, 3, 9, 10, 11:
	default:
		return false
	}
	switch b[16] {
	case 1, 8, 16, 24, 32:
		return true
	}
	return false
}
func tgaColor(p []byte, depth int) (r, g, b, a byte) {
	a = 255
	switch depth {
	case 15, 16:
		v := binary.LittleEndian.Uint16(p)
		r = byte((int(v>>10&31) * 255) / 31)
		g = byte((int(v>>5&31) * 255) / 31)
		b = byte((int(v&31) * 255) / 31)
		a = byte((v >> 15) * 255)
	case 24:
		r, g, b = p[2], p[1], p[0]
	case 32:
		r, g, b, a = p[2], p[1], p[0], p[3]
	}
	return
}
func decodeTGA(b []byte, check SizeCheck) (Image, error) {
	w, h := int(binary.LittleEndian.Uint16(b[12:])), int(binary.LittleEndian.Uint16(b[14:]))
	if err := check(w, h); err != nil {
		return Image{}, err
	}
	typ, depth, flags := int(b[2]), int(b[16]), b[17]
	if depth == 1 && typ >= 8 {
		// Fixed Pillow decode.c passes depth/8 to the RLE decoder. At one
		// bit this is zero, so its accepted header never yields a scanline;
		// ImageFile.load reports truncation rather than decoding packed RLE.
		return Image{}, errors.New("image file is truncated (0 bytes not processed)")
	}
	baseType := typ & 7
	mode := ""
	c := 1
	switch {
	case baseType == 1 && depth == 8:
		mode = "P"
	case baseType == 2 && (depth == 16 || depth == 24):
		mode = "RGB"
		c = 3
	case baseType == 2 && depth == 32:
		mode = "RGBA"
		c = 4
	case baseType == 3 && depth == 1:
		mode = "1"
	case baseType == 3 && depth == 8:
		mode = "L"
	case baseType == 3 && depth == 16:
		mode = "LA"
		c = 2
	default:
		return Image{}, errMode
	}
	if baseType == 2 && depth == 16 {
		mode = "RGBA"
		c = 4
	}
	r := Image{Mode: mode, Width: w, Height: h, Pixels: make([]byte, w*h*c)}
	off := 18 + int(b[0])
	if off > len(b) {
		return Image{}, errInvalid
	}
	if b[1] == 1 {
		first, n, d := int(binary.LittleEndian.Uint16(b[3:])), int(binary.LittleEndian.Uint16(b[5:])), int(b[7])
		entry := (d + 7) / 8
		if first+n > 256 || entry < 2 || entry > 4 || off+n*entry > len(b) {
			return Image{}, errInvalid
		}
		r.Palette = make([]byte, 768)
		for i := 0; i < n; i++ {
			rr, g, bb, _ := tgaColor(b[off+i*entry:], d)
			copy(r.Palette[(first+i)*3:], []byte{rr, g, bb})
		}
		off += n * entry
	}
	if mode == "P" && r.Palette == nil {
		return Image{}, errInvalid
	}
	bytesPer := (depth + 7) / 8
	rowBytes := w * bytesPer
	if depth == 1 {
		rowBytes = (w + 7) / 8
	}
	total := rowBytes * h
	var raw []byte
	if typ >= 8 {
		raw = make([]byte, 0, total)
		for len(raw) < total {
			if off >= len(b) {
				return Image{}, errInvalid
			}
			packet := b[off]
			off++
			count := int(packet&127) + 1
			n := count * bytesPer
			if packet&128 != 0 {
				// TgaRleDecode.c rejects a run that crosses a packed scanline. Literal
				// packets may continue through scanlines, including mode-1 packed bytes.
				if n > rowBytes-len(raw)%rowBytes || off+bytesPer > len(b) {
					return Image{}, errInvalid
				}
				for i := 0; i < count; i++ {
					raw = append(raw, b[off:off+bytesPer]...)
				}
				off += bytesPer
			} else {
				if off+n > len(b) {
					return Image{}, errInvalid
				}
				take := min(n, total-len(raw))
				raw = append(raw, b[off:off+take]...)
				off += n
			}
		}
	} else {
		if off+total > len(b) {
			return Image{}, errInvalid
		}
		raw = b[off : off+total]
	}
	if depth == 1 {
		packed := raw
		raw = make([]byte, w*h)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				if packed[y*rowBytes+x/8]&(1<<uint(7-x%8)) != 0 {
					raw[y*w+x] = 255
				}
			}
		}
		bytesPer = 1
	}
	for i := 0; i < w*h; i++ {
		x, y := i%w, i/w
		if flags&0x10 != 0 {
			x = w - 1 - x
		}
		if flags&0x20 == 0 {
			y = h - 1 - y
		}
		dst := (y*w + x) * c
		p := raw[i*bytesPer:]
		if baseType == 2 {
			rr, g, bb, a := tgaColor(p, depth)
			r.Pixels[dst], r.Pixels[dst+1], r.Pixels[dst+2] = rr, g, bb
			if c == 4 {
				r.Pixels[dst+3] = a
			}
		} else {
			copy(r.Pixels[dst:dst+c], p[:c])
		}
	}
	return r, nil
}
