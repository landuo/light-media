package imagehash

// Default-image readers translated from the installed Pillow 12.2.0 IM,
// SPIDER, QOI, ICNS, MSP and XBM plugins. Pillow's license is retained in
// PILLOW-LICENSE.txt. BLP's reader is CC0 (Jerome Leclanche); its block-color
// arithmetic intentionally follows that reader rather than another DXT codec.
// IM/MSP/XBM: Copyright Fredrik Lundh and Secret Labs AB.
// SPIDER: Copyright Health Research Inc., William Baxter, Secret Labs AB and
// Fredrik Lundh (2004). ICNS: Copyright Bob Ippolito, Secret Labs AB, Fredrik
// Lundh, Alastair Houghton and Pan Jing (2004–2020).

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

func decodeAdditional(b []byte) (Raster, bool, error) {
	var r Raster
	var err error
	switch {
	case bytes.HasPrefix(b, []byte("qoif")):
		r, err = additionalQOI(b)
	case bytes.HasPrefix(b, []byte("icns")):
		r, err = additionalICNS(b)
	case bytes.HasPrefix(b, []byte("BLP1")), bytes.HasPrefix(b, []byte("BLP2")):
		r, err = additionalBLP(b)
	case bytes.HasPrefix(b, []byte("DanM")), bytes.HasPrefix(b, []byte("LinS")):
		r, err = additionalMSP(b)
	case bytes.HasPrefix(bytes.TrimLeft(b, " \t\r\n\v\f"), []byte("#define")):
		r, err = additionalXBM(b)
	default:
		if imHeader(b) {
			r, err = additionalIM(b)
		} else if order, h := spiderHeader(b); h != nil {
			r, err = additionalSpider(b, order, h)
		} else {
			return Raster{}, false, nil
		}
	}
	return r, true, err
}

func additionalRaster(mode string, w, h int) (Raster, error) {
	if err := checkImageSize(w, h); err != nil {
		return Raster{}, err
	}
	c := channels(mode)
	if c == 0 || w > int(^uint(0)>>1)/h/c {
		return Raster{}, ErrInvalid
	}
	return Raster{Mode: mode, Width: w, Height: h, Pixels: make([]byte, w*h*c)}, nil
}
func additionalNeed(b []byte, at, n int) bool {
	return at >= 0 && n >= 0 && at <= len(b) && n <= len(b)-at
}
func additionalBits(dst []byte, b []byte, w, h, bits int, lsb, bottom bool) error {
	stride := (w*bits + 7) / 8
	if !additionalNeed(b, 0, stride*h) {
		return ErrInvalid
	}
	for y := 0; y < h; y++ {
		sy := y
		if bottom {
			sy = h - 1 - y
		}
		row := b[sy*stride:]
		for x := 0; x < w; x++ {
			v := 0
			for k := 0; k < bits; k++ {
				bit := x*bits + k
				shift := 7 - bit%8
				if lsb {
					shift = bit % 8
				}
				v = (v << 1) | int(row[bit/8]>>shift&1)
			}
			if bits == 1 && v != 0 {
				v = 255
			}
			dst[y*w+x] = byte(v)
		}
	}
	return nil
}

func additionalQOI(b []byte) (Raster, error) {
	if len(b) < 14 {
		return Raster{}, ErrInvalid
	}
	mode := "RGBA"
	if b[12] == 3 {
		mode = "RGB"
	}
	r, err := additionalRaster(mode, int(binary.BigEndian.Uint32(b[4:8])), int(binary.BigEndian.Uint32(b[8:12])))
	if err != nil {
		return r, err
	}
	c := channels(mode)
	last := [4]byte{0, 0, 0, 255}
	var seen [64][4]byte
	at := 14
	for i := 0; i < r.Width*r.Height; {
		if at >= len(b) {
			return Raster{}, ErrInvalid
		}
		op := b[at]
		at++
		run := 1
		switch {
		case op == 254:
			if !additionalNeed(b, at, 3) {
				return Raster{}, ErrInvalid
			}
			copy(last[:3], b[at:at+3])
			at += 3
		case op == 255:
			if !additionalNeed(b, at, 4) {
				return Raster{}, ErrInvalid
			}
			copy(last[:], b[at:at+4])
			at += 4
		default:
			switch op >> 6 {
			case 0:
				last = seen[op&63]
			case 1:
				last[0] += byte(int(op>>4&3) - 2)
				last[1] += byte(int(op>>2&3) - 2)
				last[2] += byte(int(op&3) - 2)
			case 2:
				if at >= len(b) {
					return Raster{}, ErrInvalid
				}
				v := b[at]
				at++
				dg := int(op&63) - 32
				last[0] += byte(dg + int(v>>4) - 8)
				last[1] += byte(dg)
				last[2] += byte(dg + int(v&15) - 8)
			case 3:
				run = int(op&63) + 1
			}
		}
		if op < 192 || op >= 254 {
			seen[(int(last[0])*3+int(last[1])*5+int(last[2])*7+int(last[3])*11)%64] = last
		}
		// Pillow consumes no end marker and ignores surplus pixels in the final run.
		for j := 0; j < run && i < r.Width*r.Height; j++ {
			copy(r.Pixels[i*c:], last[:c])
			i++
		}
	}
	return r, nil
}

func spiderHeader(b []byte) (binary.ByteOrder, []float64) {
	if len(b) < 108 {
		return nil, nil
	}
	for _, order := range []binary.ByteOrder{binary.BigEndian, binary.LittleEndian} {
		h := make([]float64, 28)
		for i := 1; i <= 27; i++ {
			h[i] = float64(math.Float32frombits(order.Uint32(b[(i-1)*4:])))
		}
		valid := true
		for _, i := range []int{1, 2, 5, 12, 13, 22, 23} {
			if math.IsNaN(h[i]) || math.IsInf(h[i], 0) || math.Trunc(h[i]) != h[i] {
				valid = false
			}
		}
		form := h[5]
		if form != 1 && form != 3 && form != -11 && form != -12 && form != -21 && form != -22 {
			valid = false
		}
		if valid && h[22] > 0 && h[22] == h[13]*h[23] {
			return order, h
		}
	}
	return nil, nil
}
func additionalSpider(b []byte, order binary.ByteOrder, h []float64) (Raster, error) {
	if h[5] != 1 {
		return Raster{}, fmt.Errorf("not a Spider 2D image")
	}
	r, err := additionalRaster("F", int(h[12]), int(h[2]))
	if err != nil {
		return r, err
	}
	off := h[22]
	if h[24] > 0 && h[27] == 0 {
		off *= 2
	} else if h[24] != 0 || h[27] != 0 {
		return Raster{}, fmt.Errorf("inconsistent stack header values")
	}
	if off > float64(len(b)) || off < 0 || !additionalNeed(b, int(off), len(r.Pixels)) {
		return Raster{}, ErrInvalid
	}
	for i := 0; i < len(r.Pixels); i += 4 {
		binary.LittleEndian.PutUint32(r.Pixels[i:], order.Uint32(b[int(off)+i:]))
	}
	return r, nil
}

// MSP RLE algorithm attribution: Encyclopedia of Graphics File Formats,
// O'Reilly, Creative Commons Attribution; the installed Pillow reader cites
// https://www.fileformat.info/format/mspaint/egff.htm (Eric Soroos,2017).
func additionalMSP(b []byte) (Raster, error) {
	if len(b) < 32 {
		return Raster{}, ErrInvalid
	}
	var sum uint16
	for i := 0; i < 32; i += 2 {
		sum ^= binary.LittleEndian.Uint16(b[i:])
	}
	if sum != 0 {
		return Raster{}, fmt.Errorf("bad MSP checksum")
	}
	r, err := additionalRaster("1", int(binary.LittleEndian.Uint16(b[4:])), int(binary.LittleEndian.Uint16(b[6:])))
	if err != nil {
		return r, err
	}
	data := b[32:]
	stride := (r.Width + 7) / 8
	if string(b[:4]) == "LinS" {
		if !additionalNeed(data, 0, 2*r.Height) {
			return Raster{}, ErrInvalid
		}
		at := 2 * r.Height
		packed := make([]byte, 0, stride*r.Height)
		for y := 0; y < r.Height; y++ {
			n := int(binary.LittleEndian.Uint16(data[y*2:]))
			if n == 0 {
				packed = append(packed, bytes.Repeat([]byte{255}, stride)...)
				continue
			}
			if !additionalNeed(data, at, n) {
				return Raster{}, ErrInvalid
			}
			row := data[at : at+n]
			at += n
			for i := 0; i < len(row); {
				run := int(row[i])
				i++
				if run == 0 {
					if !additionalNeed(row, i, 2) {
						return Raster{}, ErrInvalid
					}
					count, v := int(row[i]), row[i+1]
					i += 2
					packed = append(packed, bytes.Repeat([]byte{v}, count)...)
				} else {
					if !additionalNeed(row, i, run) {
						return Raster{}, ErrInvalid
					}
					packed = append(packed, row[i:i+run]...)
					i += run
				}
			}
		}
		data = packed
	}
	if err = additionalBits(r.Pixels, data, r.Width, r.Height, 1, false, false); err != nil {
		return Raster{}, err
	}
	return r, nil
}

var additionalXBMHeader = regexp.MustCompile(`(?s)^\s*#define[ \t]+[^\r\n]*_width[ \t]+([0-9]+)[\r\n]+#define[ \t]+[^\r\n]*_height[ \t]+([0-9]+)[\r\n]+.*_bits\[\]`)
var additionalXBMByte = regexp.MustCompile(`0[xX]([0-9a-fA-F]{1,2})`)

func additionalXBM(b []byte) (Raster, error) {
	head := b[:min(len(b), 512)]
	m := additionalXBMHeader.FindSubmatchIndex(head)
	if m == nil {
		return Raster{}, ErrInvalid
	}
	w, e := strconv.Atoi(string(head[m[2]:m[3]]))
	if e != nil {
		return Raster{}, ErrInvalid
	}
	h, e := strconv.Atoi(string(head[m[4]:m[5]]))
	if e != nil {
		return Raster{}, ErrInvalid
	}
	r, err := additionalRaster("1", w, h)
	if err != nil {
		return r, err
	}
	n := ((w + 7) / 8) * h
	values := additionalXBMByte.FindAllSubmatch(b[m[1]:], n)
	if len(values) != n {
		return Raster{}, ErrInvalid
	}
	packed := make([]byte, n)
	for i, v := range values {
		x, _ := strconv.ParseUint(string(v[1]), 16, 8)
		packed[i] = byte(x)
	}
	if err = additionalBits(r.Pixels, packed, w, h, 1, true, false); err != nil {
		return Raster{}, err
	}
	return r, nil
}

func imHeader(b []byte) bool {
	head := b[:min(len(b), 100)]
	return bytes.Contains(head, []byte("\n")) && (bytes.Contains(head, []byte("Image type:")) || bytes.Contains(head, []byte("Image size (x*y):")) || bytes.HasPrefix(head, []byte("Name:")))
}
func additionalIM(b []byte) (Raster, error) {
	end := bytes.IndexByte(b, 26)
	if end < 0 {
		return Raster{}, ErrInvalid
	}
	mode, raw := "L", "L"
	w, h := 512, 512
	lut := false
	for _, line := range bytes.Split(b[:end], []byte{'\n'}) {
		line = bytes.Trim(line, "\r\x00")
		if len(line) == 0 {
			continue
		}
		if len(line) > 100 {
			return Raster{}, ErrInvalid
		}
		key, value, ok := strings.Cut(string(line), ":")
		if !ok {
			return Raster{}, ErrInvalid
		}
		value = strings.TrimSpace(value)
		switch key {
		case "Image size (x*y)":
			size := strings.FieldsFunc(value, func(c rune) bool { return c == '*' || c == ',' })
			if len(size) != 2 {
				return Raster{}, ErrInvalid
			}
			var err error
			w, err = strconv.Atoi(strings.TrimSpace(size[0]))
			if err != nil {
				return Raster{}, ErrInvalid
			}
			h, err = strconv.Atoi(strings.TrimSpace(size[1]))
			if err != nil {
				return Raster{}, ErrInvalid
			}
		case "Lut":
			lut = true
		case "Image type":
			switch value {
			case "0 1 image", "L 1 image", "B1 image":
				mode, raw = "1", "1"
			case "Greyscale image", "Grayscale image":
				mode, raw = "L", "L"
			case "B2 image":
				mode, raw = "P", "P;2"
			case "B4 image":
				mode, raw = "P", "P;4"
			case "RGB image":
				mode, raw = "RGB", "RGB;L"
			case "X 24 image":
				mode, raw = "RGB", "RGB"
			case "RGB3 image", "RYB3 image":
				mode, raw = "RGB", "RGB;T"
			case "RGBA image":
				mode, raw = "RGBA", "RGBA;L"
			case "RGBX image":
				mode, raw = "RGB", "RGBX;L"
			case "LA image":
				mode, raw = "LA", "LA;L"
			case "PA image":
				mode, raw = "LA", "PA;L"
			case "CMYK image":
				mode, raw = "CMYK", "CMYK;L"
			case "L 32S image", "L*32S image", "L 32 S image":
				mode, raw = "I", "I;32S"
			case "L 32F image", "L*32F image", "L 32 F image":
				mode, raw = "F", "F;32F"
			case "L 16 image", "L*16 image", "L 16L image", "L*16L image":
				mode, raw = "I;16", "I;16"
			case "L 16B image", "L*16B image":
				mode, raw = "I;16B", "I;16B"
			default:
				return Raster{}, fmt.Errorf("IM raw mode %q: %w", value, ErrUnsupported)
			}
		}
	}
	off := end + 1
	var palette []byte
	if lut {
		if !additionalNeed(b, off, 768) {
			return Raster{}, ErrInvalid
		}
		p := b[off : off+768]
		off += 768
		gray := true
		palette = make([]byte, 768)
		for i := 0; i < 256; i++ {
			for c := 0; c < 3; c++ {
				palette[i*3+c] = p[c*256+i]
			}
			if p[i] != p[256+i] || p[i] != p[512+i] {
				gray = false
			}
		}
		if mode == "L" || mode == "LA" || mode == "P" {
			if !gray {
				if mode == "LA" {
					return Raster{}, fmt.Errorf("IM PA palette: %w", ErrUnsupported)
				}
				mode, raw = "P", "P"
			}
		}
	}
	r, err := additionalRaster(mode, w, h)
	if err != nil {
		return r, err
	}
	r.Palette = palette
	if mode == "P" && len(r.Palette) == 0 {
		r.Palette = make([]byte, 768)
		for i := 0; i < 256; i++ {
			for c := 0; c < 3; c++ {
				r.Palette[i*3+c] = byte(i)
			}
		}
	}
	data := b[off:]
	c := channels(mode)
	if raw == "1" || raw == "P;2" || raw == "P;4" {
		bits := 1
		if raw == "P;2" {
			bits = 2
		}
		if raw == "P;4" {
			bits = 4
		}
		if err = additionalBits(r.Pixels, data, w, h, bits, false, true); err != nil {
			return Raster{}, err
		}
		return r, nil
	}
	stride := w * c
	if raw == "RGBX;L" {
		stride = w * 4
	}
	if !additionalNeed(data, 0, stride*h) {
		return Raster{}, ErrInvalid
	}
	planar := strings.HasSuffix(raw, ";L")
	for y := 0; y < h; y++ {
		sy := h - 1 - y
		row := data[sy*stride:]
		for x := 0; x < w; x++ {
			dst := r.Pixels[(y*w+x)*c:]
			if raw == "RGB;T" {
				for ch, plane := range []int{1, 0, 2} {
					dst[ch] = data[plane*w*h+sy*w+x]
				}
			} else if planar {
				for ch := 0; ch < c; ch++ {
					dst[ch] = row[ch*w+x]
				}
			} else {
				copy(dst[:c], row[x*c:x*c+c])
			}
		}
	}
	return r, nil
}

type additionalIconSize struct {
	width, scale        int
	embedded, rgb, mask string
	skip4               bool
}

func additionalICNS(b []byte) (Raster, error) {
	if len(b) < 8 {
		return Raster{}, ErrInvalid
	}
	end := int(binary.BigEndian.Uint32(b[4:]))
	if end < 8 || end > len(b) {
		return Raster{}, ErrInvalid
	}
	blocks := map[string][]byte{}
	for at := 8; at < end; {
		if !additionalNeed(b, at, 8) {
			return Raster{}, ErrInvalid
		}
		n := int(binary.BigEndian.Uint32(b[at+4:]))
		if n < 8 || !additionalNeed(b, at, n) || at+n > end {
			return Raster{}, ErrInvalid
		}
		blocks[string(b[at:at+4])] = b[at+8 : at+n]
		at += n
	}
	// Python max compares the complete (width,height,scale) tuple, not just
	// the physical pixel area: a512x1 resource wins over256x2.
	sizes := []additionalIconSize{{512, 2, "ic10", "", "", false}, {512, 1, "ic09", "", "", false}, {256, 2, "ic14", "", "", false}, {256, 1, "ic08", "", "", false}, {128, 2, "ic13", "", "", false}, {128, 1, "ic07", "it32", "t8mk", true}, {64, 1, "icp6", "", "", false}, {32, 2, "ic12", "", "", false}, {48, 1, "", "ih32", "h8mk", false}, {32, 1, "icp5", "il32", "l8mk", false}, {16, 2, "ic11", "", "", false}, {16, 1, "icp4", "is32", "s8mk", false}}
	// Keep the source tuple ordering (48 before32x2 despite physical64).
	for i := 0; i < len(sizes); i++ {
		for j := i + 1; j < len(sizes); j++ {
			if sizes[j].width > sizes[i].width || (sizes[j].width == sizes[i].width && sizes[j].scale > sizes[i].scale) {
				sizes[i], sizes[j] = sizes[j], sizes[i]
			}
		}
	}
	for _, s := range sizes {
		payload, hasEmbedded := blocks[s.embedded]
		rgb, hasRGB := blocks[s.rgb]
		mask, hasMask := blocks[s.mask]
		if !hasEmbedded && !hasRGB && !hasMask {
			continue
		}
		w := s.width * s.scale
		if err := checkImageSize(w, w); err != nil {
			return Raster{}, err
		}
		var embedded Raster
		var err error
		if hasEmbedded {
			if !bytes.HasPrefix(payload, []byte("\x89PNG\r\n\x1a\n")) && !(len(payload) >= 2 && payload[0] == 255 && payload[1] == 79) && !bytes.HasPrefix(payload, []byte("\x00\x00\x00\x0cjP  \r\n\x87\n")) {
				return Raster{}, ErrUnsupported
			}
			embedded, err = Decode(payload)
			if err != nil {
				return Raster{}, err
			}
			if !bytes.HasPrefix(payload, []byte("\x89PNG\r\n\x1a\n")) {
				embedded, err = additionalRGBA(embedded)
				if err != nil {
					return Raster{}, err
				}
			}
		}
		var raster Raster
		if hasRGB {
			if s.skip4 {
				if len(rgb) < 4 || !bytes.Equal(rgb[:4], []byte{0, 0, 0, 0}) {
					return Raster{}, ErrInvalid
				}
				rgb = rgb[4:]
			}
			raster, err = additionalRaster("RGB", w, w)
			if err != nil {
				return Raster{}, err
			}
			n := w * w
			if len(rgb) == n*3 {
				copy(raster.Pixels, rgb)
			} else {
				at := 0
				for ch := 0; ch < 3; ch++ {
					for i := 0; i < n; {
						if at >= len(rgb) {
							return Raster{}, ErrInvalid
						}
						v := rgb[at]
						at++
						run := int(v) + 1
						repeat := v&128 != 0
						if repeat {
							run = int(v) - 125
						}
						if run > n-i {
							return Raster{}, ErrInvalid
						}
						if repeat {
							if at >= len(rgb) {
								return Raster{}, ErrInvalid
							}
							for j := 0; j < run; j++ {
								raster.Pixels[(i+j)*3+ch] = rgb[at]
							}
							at++
						} else {
							if !additionalNeed(rgb, at, run) {
								return Raster{}, ErrInvalid
							}
							for j := 0; j < run; j++ {
								raster.Pixels[(i+j)*3+ch] = rgb[at+j]
							}
							at += run
						}
						i += run
					}
				}
			}
		}
		if hasMask && len(mask) < w*w {
			return Raster{}, ErrInvalid
		}
		if hasEmbedded {
			return embedded, nil
		}
		if !hasRGB {
			return Raster{}, ErrInvalid
		}
		if hasMask {
			out, err := additionalRGBA(raster)
			if err != nil {
				return Raster{}, err
			}
			for i := 0; i < w*w; i++ {
				out.Pixels[i*4+3] = mask[i]
			}
			raster = out
		}
		return raster, nil
	}
	return Raster{}, fmt.Errorf("no32bit icon resources: %w", ErrUnsupported)
}
func additionalRGBA(r Raster) (Raster, error) {
	if r.Mode == "RGBA" {
		return r, nil
	}
	out, err := additionalRaster("RGBA", r.Width, r.Height)
	if err != nil {
		return Raster{}, err
	}
	c := channels(r.Mode)
	for i := 0; i < r.Width*r.Height; i++ {
		p := r.Pixels[i*c:]
		d := out.Pixels[i*4:]
		d[3] = 255
		switch r.Mode {
		case "1", "L":
			d[0], d[1], d[2] = p[0], p[0], p[0]
		case "LA":
			d[0], d[1], d[2], d[3] = p[0], p[0], p[0], p[1]
		case "P":
			if len(r.Palette) != 768 {
				return Raster{}, ErrInvalid
			}
			copy(d[:3], r.Palette[int(p[0])*3:int(p[0])*3+3])
		case "RGB", "RGBX":
			copy(d[:3], p[:3])
		case "CMYK":
			nk := byte(255 - p[3])
			for ch := 0; ch < 3; ch++ {
				d[ch] = nk - mul255(p[ch], nk)
			}
		case "I;16", "I;16B":
			order := binary.ByteOrder(binary.LittleEndian)
			if r.Mode == "I;16B" {
				order = binary.BigEndian
			}
			v := order.Uint16(p)
			g := byte(min(v, 255))
			d[0], d[1], d[2] = g, g, g
		case "I", "F":
			v := scalarValue(p, r.Mode)
			g := byte(0)
			if v >= 255 {
				g = 255
			} else if v > 0 {
				g = byte(v)
			}
			d[0], d[1], d[2] = g, g, g
		default:
			return Raster{}, ErrUnsupported
		}
	}
	return out, nil
}

func additionalBLP(b []byte) (Raster, error) {
	if len(b) < 20 {
		return Raster{}, ErrInvalid
	}
	old := string(b[:4]) == "BLP1"
	compression := binary.LittleEndian.Uint32(b[4:])
	encoding, alpha, alphaEncoding := int(b[8]), b[9] != 0, int(b[10])
	header := 20
	if old {
		if len(b) < 28 {
			return Raster{}, ErrInvalid
		}
		alpha = binary.LittleEndian.Uint32(b[8:]) != 0
		encoding = int(binary.LittleEndian.Uint32(b[20:]))
		header = 28
	}
	mode := "RGB"
	if alpha {
		mode = "RGBA"
	}
	r, err := additionalRaster(mode, int(binary.LittleEndian.Uint32(b[12:])), int(binary.LittleEndian.Uint32(b[16:])))
	if err != nil {
		return r, err
	}
	if !additionalNeed(b, header, 128) {
		return Raster{}, ErrInvalid
	}
	off := int(binary.LittleEndian.Uint32(b[header:]))
	n := int(binary.LittleEndian.Uint32(b[header+64:]))
	paletteAt := header + 128
	if old && compression == 1 && (encoding == 4 || encoding == 5) {
		// Pillow BLP1 reads palette indices consecutively after its palette,
		// without seeking the mipmap offset. Its own writer still computes
		// that offset with the shorter BLP2 header, eight bytes before here.
		off = paletteAt + 1024
	}
	if !additionalNeed(b, off, n) {
		return Raster{}, ErrInvalid
	}
	data := b[off : off+n]
	c := channels(mode)
	if old && compression == 0 {
		if !additionalNeed(b, paletteAt, 4) {
			return Raster{}, ErrInvalid
		}
		hn := int(binary.LittleEndian.Uint32(b[paletteAt:]))
		if !additionalNeed(b, paletteAt+4, hn) {
			return Raster{}, ErrInvalid
		}
		encoded := append(append([]byte(nil), b[paletteAt+4:paletteAt+4+hn]...), data...)
		jpeg, err := DecodeJPEG(encoded)
		if err != nil {
			return Raster{}, err
		}
		rgba, err := additionalRGBA(jpeg)
		if err != nil {
			return Raster{}, err
		}
		if jpeg.Width != r.Width || jpeg.Height != r.Height {
			return Raster{}, ErrInvalid
		}
		for i := 0; i < r.Width*r.Height; i++ {
			p := rgba.Pixels[i*4:]
			copy(r.Pixels[i*c:], []byte{p[2], p[1], p[0], 255}[:c])
		}
		return r, nil
	}
	if compression != 1 {
		return Raster{}, fmt.Errorf("BLP compression%d: %w", compression, ErrUnsupported)
	}
	if encoding == 1 || old && (encoding == 4 || encoding == 5) {
		if !additionalNeed(b, paletteAt, 1024) || len(data) < r.Width*r.Height {
			return Raster{}, ErrInvalid
		}
		palette := b[paletteAt : paletteAt+1024]
		for i := 0; i < r.Width*r.Height; i++ {
			p := palette[int(data[i])*4:]
			d := r.Pixels[i*c:]
			d[0], d[1], d[2] = p[2], p[1], p[0]
			if alpha {
				d[3] = p[3]
			}
		}
		return r, nil
	}
	if !old && encoding == 2 {
		decoded, err := additionalDXT(data, r.Width, r.Height, alphaEncoding, alpha)
		if err != nil {
			return Raster{}, err
		}
		if len(decoded) < len(r.Pixels) {
			return Raster{}, ErrInvalid
		}
		copy(r.Pixels, decoded)
		return r, nil
	}
	return Raster{}, fmt.Errorf("BLP encoding%d: %w", encoding, ErrUnsupported)
}
func additionalDXT(data []byte, w, h, kind int, alpha bool) ([]byte, error) {
	blockBytes, c := 16, 4
	if kind == 0 {
		blockBytes = 8
		c = 3
		if alpha {
			c = 4
		}
	} else if kind != 1 && kind != 7 {
		return nil, ErrUnsupported
	}
	bw, bh := (w+3)/4, (h+3)/4
	if len(data) < bw*bh*blockBytes {
		return nil, ErrInvalid
	}
	out := make([]byte, 0, bw*4*bh*4*c)
	for by := 0; by < bh; by++ {
		lines := [4][]byte{}
		for bx := 0; bx < bw; bx++ {
			block := data[(by*bw+bx)*blockBytes:]
			colorAt := 0
			if kind != 0 {
				colorAt = 8
			}
			a0 := binary.LittleEndian.Uint16(block[colorAt:])
			a1 := binary.LittleEndian.Uint16(block[colorAt+2:])
			control := binary.LittleEndian.Uint32(block[colorAt+4:])
			colors := [4][3]byte{}
			for i, v := range []uint16{a0, a1} {
				colors[i] = [3]byte{byte(v>>11) << 3, byte(v>>5&63) << 2, byte(v&31) << 3}
			}
			for ch := 0; ch < 3; ch++ {
				x, y := int(colors[0][ch]), int(colors[1][ch])
				if kind != 0 || a0 > a1 {
					colors[2][ch] = byte((2*x + y) / 3)
					colors[3][ch] = byte((x + 2*y) / 3)
				} else {
					colors[2][ch] = byte((x + y) / 2)
				}
			}
			var abits uint64
			if kind == 7 {
				for i := 0; i < 6; i++ {
					abits |= uint64(block[2+i]) << uint(i*8)
				}
			}
			for py := 0; py < 4; py++ {
				for px := 0; px < 4; px++ {
					i := py*4 + px
					code := int(control >> uint(i*2) & 3)
					pixel := colors[code]
					a := byte(255)
					if kind == 0 && a0 <= a1 && code == 3 {
						a = 0
					}
					if kind == 1 {
						a = byte(block[i/2]>>uint((i%2)*4)&15) * 17
					}
					if kind == 7 {
						v := int(abits >> uint(i*3) & 7)
						x, y := int(block[0]), int(block[1])
						switch {
						case v == 0:
							a = byte(x)
						case v == 1:
							a = byte(y)
						case x > y:
							a = byte(((8-v)*x + (v-1)*y) / 7)
						case v == 6:
							a = 0
						case v == 7:
							a = 255
						default:
							a = byte(((6-v)*x + (v-1)*y) / 5)
						}
					}
					lines[py] = append(lines[py], pixel[:]...)
					if c == 4 {
						lines[py] = append(lines[py], a)
					}
				}
			}
		}
		for _, line := range lines {
			out = append(out, line...)
		}
	}
	return out, nil
}
