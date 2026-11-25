package legacycodec

import "encoding/binary"

// The mode/stride rules follow Pillow 12.2.0 PcxImagePlugin._open.
func decodePCX(b []byte, check SizeCheck) (Image, error) {
	if len(b) < 128 {
		return Image{}, errInvalid
	}
	w := int(binary.LittleEndian.Uint16(b[8:])) + 1 - int(binary.LittleEndian.Uint16(b[4:]))
	h := int(binary.LittleEndian.Uint16(b[10:])) + 1 - int(binary.LittleEndian.Uint16(b[6:]))
	if err := check(w, h); err != nil {
		return Image{}, err
	}
	bits, planes := int(b[3]), int(b[65])
	mode := ""
	c := 1
	var pal []byte
	switch {
	case bits == 1 && planes == 1:
		mode = "1"
	case bits == 1 && (planes == 2 || planes == 4):
		mode = "P"
		pal = make([]byte, 768)
		copy(pal, b[16:64])
	case b[1] == 5 && bits == 8 && planes == 1:
		mode = "L"
		if len(b) >= 769 && b[len(b)-769] == 12 {
			p := b[len(b)-768:]
			for i := 0; i < 256; i++ {
				if p[i*3] != byte(i) || p[i*3+1] != byte(i) || p[i*3+2] != byte(i) {
					mode = "P"
					pal = append([]byte(nil), p...)
					break
				}
			}
		}
	case b[1] == 5 && bits == 8 && planes == 3:
		mode = "RGB"
		c = 3
	default:
		return Image{}, errMode
	}
	stride := (w*bits + 7) / 8
	if int(binary.LittleEndian.Uint16(b[66:])) != stride {
		stride += stride % 2
	}
	total := stride * planes * h
	raw := make([]byte, total)
	at, off := 0, 128
	for at < total {
		if off >= len(b) {
			return Image{}, errInvalid
		}
		v := b[off]
		off++
		count := 1
		if v&0xc0 == 0xc0 {
			count = int(v & 63)
			if off >= len(b) {
				return Image{}, errInvalid
			}
			v = b[off]
			off++
		}
		if count > total-at {
			return Image{}, errInvalid
		}
		for j := 0; j < count; j++ {
			raw[at] = v
			at++
		}
	}
	r := Image{Mode: mode, Width: w, Height: h, Pixels: make([]byte, w*h*c), Palette: pal}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst := (y*w + x) * c
			base := y * stride * planes
			if bits == 1 {
				idx := byte(0)
				for plane := 0; plane < planes; plane++ {
					idx |= ((raw[base+plane*stride+x/8] >> uint(7-x%8)) & 1) << uint(plane)
				}
				if mode == "1" {
					idx *= 255
				}
				r.Pixels[dst] = idx
			} else {
				for ch := 0; ch < c; ch++ {
					r.Pixels[dst+ch] = raw[base+ch*stride+x]
				}
			}
		}
	}
	return r, nil
}
