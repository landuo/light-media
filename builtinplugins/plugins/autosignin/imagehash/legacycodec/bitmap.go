package legacycodec

import (
	"bytes"
	"encoding/binary"
	"light-media/builtinplugins/plugins/autosignin/imagehash/legacycodec/dib"
	"image"
	"image/png"
	"math/bits"
	"sort"
)

func dibSize(b []byte) (int, int, error) {
	if len(b) < 12 {
		return 0, 0, errInvalid
	}
	size := int(binary.LittleEndian.Uint32(b))
	if size > len(b) {
		return 0, 0, errInvalid
	}
	if size == 12 {
		return int(binary.LittleEndian.Uint16(b[4:])), int(binary.LittleEndian.Uint16(b[6:])), nil
	}
	if size < 40 {
		return 0, 0, errInvalid
	}
	w, h := int(int32(binary.LittleEndian.Uint32(b[4:]))), int(int32(binary.LittleEndian.Uint32(b[8:])))
	if h < 0 {
		h = -h
	}
	return w, h, nil
}
func decodeDIB(b []byte, check SizeCheck, icon bool) (Image, error) {
	w, h, err := dibSize(b)
	if err != nil {
		return Image{}, err
	}
	if err = check(w, h); err != nil {
		return Image{}, err
	}
	opts := dib.Options{}
	if icon {
		opts.IconSize = image.Pt(w, h/2)
	}
	result, err := dib.Decode(b, opts)
	if err != nil {
		return Image{}, err
	}
	mode := paletteMode(result.Image, int(result.BitCount))
	if icon {
		mode = "RGBA"
	} else if _, ok := result.Image.(*image.Paletted); !ok {
		mode = "RGB"
		if len(b) >= 56 && binary.LittleEndian.Uint32(b[16:]) == 3 && binary.LittleEndian.Uint32(b[52:]) != 0 {
			mode = "RGBA"
		}
	}
	return fromImage(result.Image, mode), nil
}
func decodeICO(b []byte, check SizeCheck) (Image, error) {
	if len(b) < 6 {
		return Image{}, errInvalid
	}
	n := int(binary.LittleEndian.Uint16(b[4:]))
	if n == 0 || n > (len(b)-6)/16 {
		return Image{}, errInvalid
	}
	type entry struct{ w, h, depth, off, size int }
	entries := make([]entry, 0, n)
	for i := 0; i < n; i++ {
		p := b[6+i*16:]
		w, h := int(p[0]), int(p[1])
		if w == 0 {
			w = 256
		}
		if h == 0 {
			h = 256
		}
		depth := int(binary.LittleEndian.Uint16(p[6:]))
		if depth == 0 {
			if p[2] == 0 {
				depth = 256
			} else {
				depth = bits.Len(uint(p[2] - 1))
			}
		}
		off, size := uint64(binary.LittleEndian.Uint32(p[12:])), uint64(binary.LittleEndian.Uint32(p[8:]))
		if off > uint64(len(b)) || size > uint64(len(b))-off {
			return Image{}, errInvalid
		}
		entries = append(entries, entry{w, h, depth, int(off), int(size)})
	}
	// Pillow sorts depth ascending, then area descending, using stable sorts.
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].depth < entries[j].depth })
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].w*entries[i].h > entries[j].w*entries[j].h })
	e := entries[0]
	if err := check(e.w, e.h); err != nil {
		return Image{}, err
	}
	data := b[e.off : e.off+e.size]
	if len(data) >= 26 && string(data[:8]) == "\x89PNG\r\n\x1a\n" {
		cfg, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return Image{}, err
		}
		if err = check(cfg.Width, cfg.Height); err != nil {
			return Image{}, err
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			return Image{}, err
		}
		mode := ""
		switch data[25] {
		case 0:
			mode = "L"
			if data[24] == 1 {
				mode = "1"
			}
			if data[24] == 16 {
				mode = "I;16"
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
			return Image{}, errMode
		}
		return fromImage(img, mode), nil
	}
	w, h, err := dibSize(data)
	if err != nil {
		return Image{}, err
	}
	if err = check(w, h); err != nil {
		return Image{}, err
	}
	if h%2 != 0 {
		return Image{}, errInvalid
	}
	h /= 2
	decoded, err := dib.Decode(data, dib.Options{IconSize: image.Pt(w, h)})
	if err != nil {
		return Image{}, err
	}
	r := fromImage(decoded.Image, "RGBA")
	// Pillow always uses the stored 32-bit alpha. Other depths use the bottom-up
	// one-bit AND mask; a set bit makes the corresponding pixel transparent.
	if decoded.BitCount != 32 {
		stride := (w + 31) / 32 * 4
		mask := len(data) - stride*h
		if mask < decoded.PixelEnd {
			return Image{}, errInvalid
		}
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				if data[mask+(h-1-y)*stride+x/8]&(1<<uint(7-x%8)) != 0 {
					r.Pixels[(y*w+x)*4+3] = 0
				}
			}
		}
	}
	return r, nil
}
