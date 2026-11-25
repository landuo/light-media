package legacycodec

import "encoding/binary"

// Pillow SGI raw and RLE rows are channel-planar and start at bottom left.
// Its 16-bit SGI decoder loads the high sample byte into an 8-bit L band.
func decodeSGI(b []byte, check SizeCheck) (Image, error) {
	if len(b) < 512 {
		return Image{}, errInvalid
	}
	storage, bpc := int(b[2]), int(b[3])
	dim := int(binary.BigEndian.Uint16(b[4:]))
	w, h, c := int(binary.BigEndian.Uint16(b[6:])), int(binary.BigEndian.Uint16(b[8:])), int(binary.BigEndian.Uint16(b[10:]))
	if err := check(w, h); err != nil {
		return Image{}, err
	}
	mode := ""
	switch {
	case (dim == 1 || dim == 2) && c == 1:
		mode = "L"
	case dim == 3 && c == 3:
		mode = "RGB"
	case dim == 3 && c == 4:
		mode = "RGBA"
	default:
		return Image{}, errMode
	}
	if bpc != 1 && bpc != 2 {
		return Image{}, errMode
	}
	r := Image{Mode: mode, Width: w, Height: h, Pixels: make([]byte, w*h*c)}
	if storage == 0 {
		if (len(b)-512)/bpc < w*h*c {
			return Image{}, errInvalid
		}
		for ch := 0; ch < c; ch++ {
			for y := 0; y < h; y++ {
				for x := 0; x < w; x++ {
					r.Pixels[((h-1-y)*w+x)*c+ch] = b[512+((ch*h+y)*w+x)*bpc]
				}
			}
		}
		return r, nil
	}
	if storage != 1 {
		return Image{}, errMode
	}
	n := h * c
	if n > (len(b)-512)/8 {
		return Image{}, errInvalid
	}
	for ch := 0; ch < c; ch++ {
		for y := 0; y < h; y++ {
			i := ch*h + y
			off := uint64(binary.BigEndian.Uint32(b[512+i*4:]))
			size := uint64(binary.BigEndian.Uint32(b[512+n*4+i*4:]))
			if off > uint64(len(b)) || size > uint64(len(b))-off {
				return Image{}, errInvalid
			}
			line := b[int(off):int(off+size)]
			pos, x := 0, 0
			for {
				if pos+bpc > len(line) {
					return Image{}, errInvalid
				}
				control := int(line[pos])
				if bpc == 2 {
					control = int(binary.BigEndian.Uint16(line[pos:]))
				}
				pos += bpc
				count := control & 127
				if count == 0 {
					break
				}
				if count > w-x {
					return Image{}, errInvalid
				}
				if control&128 != 0 {
					if pos+count*bpc > len(line) {
						return Image{}, errInvalid
					}
					for j := 0; j < count; j++ {
						r.Pixels[((h-1-y)*w+x)*c+ch] = line[pos]
						pos += bpc
						x++
					}
				} else {
					if pos+bpc > len(line) {
						return Image{}, errInvalid
					}
					v := line[pos]
					pos += bpc
					for j := 0; j < count; j++ {
						r.Pixels[((h-1-y)*w+x)*c+ch] = v
						x++
					}
				}
			}
			if x != w {
				return Image{}, errInvalid
			}
		}
	}
	return r, nil
}
