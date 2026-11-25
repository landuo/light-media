package legacycodec

import (
	"encoding/binary"
	"fmt"
	"light-media/builtinplugins/plugins/autosignin/imagehash/legacycodec/dds"
	"math/bits"
)

func decodeDDS(b []byte, check SizeCheck) (Image, error) {
	if len(b) < 128 || binary.LittleEndian.Uint32(b[4:]) != 124 {
		return Image{}, errInvalid
	}
	w, h := int(binary.LittleEndian.Uint32(b[16:])), int(binary.LittleEndian.Uint32(b[12:]))
	if err := check(w, h); err != nil {
		return Image{}, err
	}
	flags, depth := binary.LittleEndian.Uint32(b[80:]), int(binary.LittleEndian.Uint32(b[88:]))
	mode := ""
	c := 1
	off := 128
	if flags&0x40 != 0 {
		mode = "RGB"
		c = 3
		if flags&1 != 0 {
			mode = "RGBA"
			c = 4
		}
		n := depth / 8
		if n < 1 || n > 4 {
			return Image{}, errMode
		}
		if w*h > (len(b)-off)/n {
			return Image{}, errInvalid
		}
		r := Image{Mode: mode, Width: w, Height: h, Pixels: make([]byte, w*h*c)}
		for i := 0; i < w*h; i++ {
			var v uint32
			for j := 0; j < n; j++ {
				v |= uint32(b[off+i*n+j]) << uint(j*8)
			}
			for ch := 0; ch < c; ch++ {
				mask := binary.LittleEndian.Uint32(b[92+ch*4:])
				if mask == 0 {
					continue
				}
				shift := bits.TrailingZeros32(mask)
				total := mask >> shift
				part := (v & mask) >> shift
				r.Pixels[i*c+ch] = byte(float64(part) / float64(total) * 255)
			}
		}
		return r, nil
	}
	if flags&0x20000 != 0 {
		if depth == 8 {
			mode = "L"
		} else if depth == 16 && flags&1 != 0 {
			mode = "LA"
			c = 2
		} else {
			return Image{}, errMode
		}
	} else if flags&0x20 != 0 {
		mode = "P"
		if len(b)-off < 1024 {
			return Image{}, errInvalid
		}
		r := Image{Mode: mode, Width: w, Height: h, Pixels: make([]byte, w*h), Palette: make([]byte, 768)}
		for i := 0; i < 256; i++ {
			copy(r.Palette[i*3:], b[off+i*4:off+i*4+3])
		}
		off += 1024
		if len(b)-off < w*h {
			return Image{}, errInvalid
		}
		copy(r.Pixels, b[off:off+w*h])
		return r, nil
	} else if flags&4 != 0 {
		compression := string(b[84:88])
		outputMode := "RGBA"
		switch compression {
		case "BC4U", "ATI1":
			compression, outputMode = "BC4", "L"
		case "BC5U", "ATI2":
			compression, outputMode = "BC5", "RGB"
		case "BC5S":
			outputMode = "RGB"
		}
		if compression == "DX10" {
			if len(b) < 148 {
				return Image{}, errInvalid
			}
			format := binary.LittleEndian.Uint32(b[128:])
			off = 148
			switch format {
			case 70, 71:
				compression = "DXT1"
			case 73, 74:
				compression = "DXT3"
			case 76, 77:
				compression = "DXT5"
			case 79, 80:
				compression, outputMode = "BC4", "L"
			case 82, 83:
				compression, outputMode = "BC5", "RGB"
			case 84:
				compression, outputMode = "BC5S", "RGB"
			case 95:
				compression, outputMode = "BC6H", "RGB"
			case 96:
				compression, outputMode = "BC6HS", "RGB"
			case 97, 98, 99:
				compression = "BC7"
			case 27, 28, 29:
				mode = "RGBA"
				c = 4
			default:
				return Image{}, fmt.Errorf("unsupported DDS DXGI format %d", format)
			}
		}
		if mode == "" {
			img, err := dds.Decode(b[off:], w, h, compression)
			if err != nil {
				return Image{}, err
			}
			return fromImage(img, outputMode), nil
		}
	} else {
		return Image{}, errMode
	}
	if w*h > (len(b)-off)/c {
		return Image{}, errInvalid
	}
	return Image{Mode: mode, Width: w, Height: h, Pixels: append([]byte(nil), b[off:off+w*h*c]...)}, nil
}
