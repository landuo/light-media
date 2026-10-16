package imagehash

import (
	"bytes"
	"errors"
	"fmt"

	"light-media/internal/plugins/autosignin/imagehash/jpegport"
)

// DecodeJPEG follows Pillow's JpegImagePlugin._accept gate before the
// libjpeg-compatible 8-bit Huffman decoder. It returns native L/RGB/CMYK bytes.
func DecodeJPEG(encoded []byte) (Raster, error) {
	if len(encoded) < 3 || len(encoded) > 8<<20 || encoded[0] != 0xff || encoded[1] != 0xd8 || encoded[2] != 0xff {
		return Raster{}, ErrInvalid
	}
	r, e := jpeg.DecodeRaster(bytes.NewReader(encoded))
	if e != nil {
		var unsupported jpeg.UnsupportedError
		if errors.As(e, &unsupported) {
			return Raster{}, fmt.Errorf("%w: %s", ErrUnsupported, string(unsupported))
		}
		var budget jpeg.ResourceLimitError
		if errors.As(e, &budget) {
			return Raster{}, fmt.Errorf("%w: %s", ErrInvalid, string(budget))
		}
		return Raster{}, e
	}
	return Raster{Mode: r.Mode, Width: r.Width, Height: r.Height, Pixels: r.Pixels}, nil
}
