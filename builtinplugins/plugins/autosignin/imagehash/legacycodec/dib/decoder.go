// Package dib decodes the shared Windows DIB pixel representation used by BMP
// and ICO. Container headers, icon selection and icon AND masks live elsewhere.
package dib

import (
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"math/bits"

	"light-media/builtinplugins/plugins/autosignin/imagehash/legacycodec/internal/imagelimit"
)

const (
	rgb            = 0
	rle8           = 1
	rle4           = 2
	bitfields      = 3
	alphaBitfields = 6
)

// Options makes container-specific interpretation explicit.
type Options struct {
	// PixelOffset is relative to the beginning of the DIB. Zero means pixels
	// immediately follow the header, masks and palette (packed DIBs in ICO).
	PixelOffset int
	// IconSize requests ICO's positive doubled header height and legacy BGRA
	// interpretation of BI_RGB/32. A zero point means an ordinary bitmap.
	IconSize image.Point
}

type Result struct {
	Image    image.Image
	BitCount uint16
	// PixelEnd is the byte offset after the XOR/color data, relative to the DIB.
	PixelEnd int
	// LegacyAlpha allows ICO's all-zero-alpha fallback only for BI_RGB/32.
	LegacyAlpha bool
}

type channel struct {
	mask  uint32
	shift int
	max   uint32
}

func (c channel) value(pixel uint32) uint8 {
	if c.mask == 0 {
		return 255
	}
	v := uint64((pixel & c.mask) >> c.shift)
	return uint8((v*255 + uint64(c.max)/2) / uint64(c.max))
}

type header struct {
	width, height, depth int
	topDown, legacyAlpha bool
	compression          uint32
	imageSize            uint32
	start                int
	palette              color.Palette
	channels             [4]channel
}

// Decode validates the DIB before allocating its output image. Indexed data is
// returned as image.Paletted, direct color as image.NRGBA.
func Decode(data []byte, opts Options) (*Result, error) {
	h, err := parse(data, opts)
	if err != nil {
		return nil, err
	}
	if h.compression == rle4 || h.compression == rle8 {
		return h.decodeRLE(data)
	}
	stride, err := Stride(h.width, h.depth)
	if err != nil {
		return nil, err
	}
	size, err := imagelimit.Bytes(stride, h.height)
	if err != nil {
		return nil, err
	}
	if size > len(data)-h.start {
		return nil, errors.New("truncated DIB pixels")
	}
	result := &Result{BitCount: uint16(h.depth), PixelEnd: h.start + size, LegacyAlpha: h.legacyAlpha}
	bounds := image.Rect(0, 0, h.width, h.height)
	if h.depth <= 8 {
		img := image.NewPaletted(bounds, h.palette)
		for row := 0; row < h.height; row++ {
			line := data[h.start+row*stride : h.start+(row+1)*stride]
			y := h.y(row)
			for x := 0; x < h.width; x++ {
				index := line[x*h.depth/8] >> uint(8-h.depth-(x*h.depth)%8) & byte((1<<h.depth)-1)
				if int(index) >= len(h.palette) {
					return nil, errors.New("DIB palette index out of range")
				}
				img.SetColorIndex(x, y, index)
			}
		}
		result.Image = img
	} else {
		if _, err := imagelimit.Bytes(h.width, h.height, 4); err != nil {
			return nil, err
		}
		img := image.NewNRGBA(bounds)
		for row := 0; row < h.height; row++ {
			line := data[h.start+row*stride : h.start+(row+1)*stride]
			y := h.y(row)
			for x := 0; x < h.width; x++ {
				var c color.NRGBA
				if h.depth == 24 {
					p := line[x*3:]
					c = color.NRGBA{R: p[2], G: p[1], B: p[0], A: 255}
				} else {
					var v uint32
					if h.depth == 16 {
						v = uint32(binary.LittleEndian.Uint16(line[x*2:]))
					} else {
						v = binary.LittleEndian.Uint32(line[x*4:])
					}
					c = color.NRGBA{R: h.channels[0].value(v), G: h.channels[1].value(v), B: h.channels[2].value(v), A: h.channels[3].value(v)}
				}
				img.SetNRGBA(x, y, c)
			}
		}
		result.Image = img
	}
	return result, nil
}

func (h *header) y(row int) int {
	if h.topDown {
		return row
	}
	return h.height - 1 - row
}

// Stride returns a DWORD-aligned scanline size after checked arithmetic.
func Stride(width, depth int) (int, error) {
	n, err := imagelimit.Mul(width, depth)
	if err != nil {
		return 0, err
	}
	n, err = imagelimit.Sum(n, 31)
	if err != nil {
		return 0, err
	}
	return imagelimit.Bytes(n/32, 4)
}

func parse(data []byte, opts Options) (*header, error) {
	return parseHeader(data, opts, false)
}

// parseHeader also supports metadata-only buffers; config parsing does not
// require the direct-color optimization table, pixel gap or raster to exist.
func parseHeader(data []byte, opts Options, configOnly bool) (*header, error) {
	if len(data) < 4 {
		return nil, errors.New("truncated DIB header")
	}
	headerSize := binary.LittleEndian.Uint32(data[:4])
	switch headerSize {
	case 12, 40, 52, 56, 64, 108, 124:
	default:
		return nil, fmt.Errorf("unsupported DIB header size %d", headerSize)
	}
	if uint64(headerSize) > uint64(len(data)) {
		return nil, errors.New("truncated DIB header")
	}
	h := &header{start: int(headerSize)}
	var width, height int64
	var colors uint64
	var planes uint16
	entrySize := 4
	if headerSize == 12 {
		width = int64(binary.LittleEndian.Uint16(data[4:6]))
		height = int64(binary.LittleEndian.Uint16(data[6:8]))
		planes = binary.LittleEndian.Uint16(data[8:10])
		h.depth = int(binary.LittleEndian.Uint16(data[10:12]))
		entrySize = 3
		if h.depth != 1 && h.depth != 4 && h.depth != 8 && h.depth != 24 {
			return nil, fmt.Errorf("unsupported CORE DIB bit depth %d", h.depth)
		}
	} else {
		width = int64(int32(binary.LittleEndian.Uint32(data[4:8])))
		height = int64(int32(binary.LittleEndian.Uint32(data[8:12])))
		planes = binary.LittleEndian.Uint16(data[12:14])
		h.depth = int(binary.LittleEndian.Uint16(data[14:16]))
		h.compression = binary.LittleEndian.Uint32(data[16:20])
		h.imageSize = binary.LittleEndian.Uint32(data[20:24])
		colors = uint64(binary.LittleEndian.Uint32(data[32:36]))
	}
	if planes != 1 {
		return nil, errors.New("invalid DIB planes")
	}
	if opts.IconSize != (image.Point{}) {
		if width != int64(opts.IconSize.X) || height != int64(opts.IconSize.Y)*2 || height <= 0 {
			return nil, errors.New("ICO DIB dimensions do not match directory (positive doubled height required)")
		}
		height /= 2
		h.legacyAlpha = h.depth == 32 && h.compression == rgb
	} else if height < 0 {
		h.topDown = true
		height = -height // int64 also handles the minimum signed 32-bit height.
	}
	if width <= 0 || height <= 0 || width > imagelimit.MaxPixels || height > imagelimit.MaxPixels {
		return nil, errors.New("invalid DIB dimensions")
	}
	h.width, h.height = int(width), int(height)
	if err := imagelimit.Dimensions(h.width, h.height); err != nil {
		return nil, err
	}
	switch h.depth {
	case 1, 4, 8, 16, 24, 32:
	default:
		return nil, fmt.Errorf("unsupported DIB bit depth %d", h.depth)
	}
	switch h.compression {
	case rgb:
	case rle4, rle8:
		if (h.compression == rle4 && h.depth != 4) || (h.compression == rle8 && h.depth != 8) {
			return nil, errors.New("DIB RLE compression does not match bit depth")
		}
		if h.topDown {
			return nil, errors.New("top-down RLE DIB is invalid")
		}
		if opts.IconSize != (image.Point{}) && h.imageSize == 0 {
			return nil, errors.New("ICO RLE DIB requires a compressed image size")
		}
	case bitfields, alphaBitfields:
		if h.depth != 16 && h.depth != 32 {
			return nil, errors.New("DIB bitfields require 16 or 32 bits")
		}
	default:
		return nil, fmt.Errorf("unsupported DIB compression %d", h.compression)
	}
	if err := h.readMasks(data, headerSize); err != nil {
		return nil, err
	}
	if h.depth <= 8 {
		if colors == 0 {
			colors = 1 << h.depth
		}
		if colors > 1<<h.depth {
			return nil, errors.New("invalid DIB palette size")
		}
	}
	// Direct-color DIBs may also carry an optimization color table. Skip it,
	// but validate its length before locating packed ICO pixel data.
	paletteBytes := colors * uint64(entrySize)
	if (!configOnly || h.depth <= 8) && paletteBytes > uint64(len(data)-h.start) {
		return nil, errors.New("truncated DIB palette")
	}
	if configOnly && paletteBytes > uint64(imagelimit.MaxBytes) {
		return nil, errors.New("DIB color table exceeds byte limit")
	}
	if h.depth <= 8 {
		h.palette = make(color.Palette, int(colors))
		for i := range h.palette {
			p := data[h.start+i*entrySize:]
			h.palette[i] = color.RGBA{R: p[2], G: p[1], B: p[0], A: 255}
		}
	}
	h.start += int(paletteBytes)
	if opts.PixelOffset != 0 {
		if opts.PixelOffset < h.start || (!configOnly && opts.PixelOffset > len(data)) {
			return nil, errors.New("invalid DIB pixel offset")
		}
		h.start = opts.PixelOffset
	}
	return h, nil
}

func (h *header) readMasks(data []byte, headerSize uint32) error {
	var masks [4]uint32
	if h.compression == bitfields || h.compression == alphaBitfields {
		count := 3
		if h.compression == alphaBitfields || headerSize >= 56 {
			count = 4
		}
		if headerSize == 40 {
			if count*4 > len(data)-h.start {
				return errors.New("truncated DIB color masks")
			}
			h.start += count * 4
		} else if headerSize < uint32(40+count*4) {
			return errors.New("DIB header has no alpha mask")
		}
		for i := 0; i < count; i++ {
			masks[i] = binary.LittleEndian.Uint32(data[40+i*4:])
		}
		if h.compression == alphaBitfields && masks[3] == 0 {
			return errors.New("missing DIB alpha mask")
		}
	} else if h.depth == 16 {
		masks = [4]uint32{0x7c00, 0x03e0, 0x001f, 0}
	} else if h.depth == 32 {
		masks = [4]uint32{0xff0000, 0xff00, 0xff, 0}
		if h.legacyAlpha {
			masks[3] = 0xff000000
		}
	} else {
		return nil
	}
	var used uint32
	for i, mask := range masks {
		if mask == 0 {
			if i < 3 {
				return errors.New("empty DIB RGB mask")
			}
			continue
		}
		if uint64(mask)>>uint(h.depth) != 0 || mask&used != 0 {
			return errors.New("overlapping or out-of-range DIB color mask")
		}
		shift := bits.TrailingZeros32(mask)
		max := mask >> shift
		if max&(max+1) != 0 {
			return errors.New("noncontiguous DIB color mask")
		}
		h.channels[i] = channel{mask, shift, max}
		used |= mask
	}
	return nil
}
