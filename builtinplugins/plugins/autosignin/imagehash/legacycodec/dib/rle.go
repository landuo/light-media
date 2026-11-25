package dib

import (
	"errors"
	"image"
)

func (h *header) decodeRLE(data []byte) (*Result, error) {
	stream := data[h.start:]
	if h.imageSize != 0 {
		if uint64(h.imageSize) > uint64(len(stream)) {
			return nil, errors.New("truncated DIB RLE data")
		}
		stream = stream[:int(h.imageSize)]
	}
	if len(stream) < 2 {
		return nil, errors.New("truncated DIB RLE command")
	}
	img := image.NewPaletted(image.Rect(0, 0, h.width, h.height), h.palette)
	consumed, err := decodeRLE(stream, img, h.depth)
	if err != nil {
		return nil, err
	}
	if h.imageSize != 0 {
		consumed = int(h.imageSize)
	}
	return &Result{Image: img, BitCount: uint16(h.depth), PixelEnd: h.start + consumed}, nil
}

// decodeRLE writes directly to the final paletted image. x/y are coordinates
// in the bottom-up stream; unwritten (skipped or early-EOB) pixels keep index 0.
// A declared stream boundary is supplied by the caller so the ICO AND mask
// cannot be consumed as compressed XOR data.
func decodeRLE(stream []byte, img *image.Paletted, depth int) (int, error) {
	width, height := img.Rect.Dx(), img.Rect.Dy()
	x, y, pos := 0, 0, 0
	write := func(index byte) error {
		if int(index) >= len(img.Palette) {
			return errors.New("DIB RLE palette index out of range")
		}
		img.SetColorIndex(x, height-1-y, index)
		x++
		return nil
	}
	for {
		if len(stream)-pos < 2 {
			return 0, errors.New("DIB RLE missing end-of-bitmap or truncated command")
		}
		count, value := int(stream[pos]), stream[pos+1]
		pos += 2
		if count != 0 {
			if y >= height || count > width-x {
				return 0, errors.New("DIB RLE run exceeds image bounds")
			}
			for i := 0; i < count; i++ {
				index := value
				if depth == 4 {
					index = value >> 4
					if i%2 != 0 {
						index = value & 15
					}
				}
				if err := write(index); err != nil {
					return 0, err
				}
			}
			continue
		}
		switch value {
		case 0: // End of line; the final EOL may be followed by EOB.
			if y >= height {
				return 0, errors.New("DIB RLE end-of-line exceeds image bounds")
			}
			x = 0
			y++
		case 1:
			return pos, nil
		case 2:
			if len(stream)-pos < 2 {
				return 0, errors.New("truncated DIB RLE delta")
			}
			x += int(stream[pos])
			y += int(stream[pos+1])
			pos += 2
			if x >= width || y >= height {
				return 0, errors.New("DIB RLE delta exceeds image bounds")
			}
		default:
			count = int(value)
			if y >= height || count > width-x {
				return 0, errors.New("DIB RLE absolute run exceeds image bounds")
			}
			bytes := count
			if depth == 4 {
				bytes = (count + 1) / 2
			}
			padded := (bytes + 1) &^ 1
			if padded > len(stream)-pos {
				return 0, errors.New("truncated DIB RLE absolute run or padding")
			}
			for i := 0; i < count; i++ {
				var index byte
				if depth == 4 {
					index = stream[pos+i/2] >> uint(4-4*(i%2)) & 15
				} else {
					index = stream[pos+i]
				}
				if err := write(index); err != nil {
					return 0, err
				}
			}
			pos += padded
		}
	}
}
