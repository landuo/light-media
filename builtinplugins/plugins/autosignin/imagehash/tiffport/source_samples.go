package tiff

import (
	"encoding/binary"
	"image"
	"image/color"
	"io"
	"math"

	jpeg "light-media/builtinplugins/plugins/autosignin/imagehash/jpegport"
)

const mScalar imageMode = mCMYK + 1

// Scalar retains Pillow's native I/F samples until after its resize. Clipping
// them through image.Gray before that operation changes the Tjupt hash.
type Scalar struct {
	Pix   []byte
	Rect  image.Rectangle
	Float bool
}

func (p *Scalar) Bounds() image.Rectangle { return p.Rect }
func (p *Scalar) ColorModel() color.Model { return color.GrayModel }
func (p *Scalar) At(x, y int) color.Color {
	if !image.Pt(x, y).In(p.Rect) {
		return color.Gray{}
	}
	v := binary.LittleEndian.Uint32(p.Pix[((y-p.Rect.Min.Y)*p.Rect.Dx()+x-p.Rect.Min.X)*4:])
	n := float64(int32(v))
	if p.Float {
		n = float64(math.Float32frombits(v))
	}
	if !(n > 0) {
		n = 0
	}
	if n > 255 {
		n = 255
	}
	return color.Gray{Y: byte(n)}
}

func (d *decoder) decodeScalarPixels(dst *Scalar, r decodeRegion) error {
	bytesPerSample := int(d.bpp / 8)
	for y := r.ymin; y < r.rMaxY; y++ {
		rowStart := d.off
		for x := r.xmin; x < r.rMaxX; x++ {
			if d.off+bytesPerSample > len(d.buf) {
				return errNoPixels
			}
			b := d.buf[d.off : d.off+bytesPerSample]
			d.off += bytesPerSample
			var value uint32
			if dst.Float {
				if bytesPerSample == 8 {
					value = math.Float32bits(float32(math.Float64frombits(d.byteOrder.Uint64(b))))
				} else {
					value = d.byteOrder.Uint32(b)
				}
			} else {
				switch bytesPerSample {
				case 1:
					value = uint32(int32(int8(b[0])))
				case 2:
					value = uint32(int32(int16(d.byteOrder.Uint16(b))))
				case 4:
					value = d.byteOrder.Uint32(b)
				default:
					return UnsupportedError("signed sample depth")
				}
			}
			binary.LittleEndian.PutUint32(dst.Pix[(y*dst.Rect.Dx()+x)*4:], value)
		}
		d.off = rowStart + r.rowBytes
	}
	return nil
}

// DecodePillow exposes the source image mode while retaining the decoder's
// strip/tile/compression implementation. The caller applies Pillow's own size
// check before any output pixel allocation.
func DecodePillow(r io.Reader, check func(int, int) error) (image.Image, string, int, error) {
	d, err := newDecoderAt(r, 0)
	if err != nil {
		return nil, "", 0, err
	}
	if err = check(d.config.Width, d.config.Height); err != nil {
		return nil, "", 0, err
	}
	mode := "RGB"
	switch d.mode {
	case mScalar:
		mode = "I"
		if d.firstVal(tSampleFormat) == 3 {
			mode = "F"
		}
	case mGray, mGrayInvert:
		mode = "L"
		if d.bpp == 1 {
			mode = "1"
		}
		if d.bpp == 16 {
			mode = "I;16"
			if d.byteOrder == binary.BigEndian {
				mode = "I;16B"
			}
		}
	case mPaletted:
		mode = "P"
	case mGrayAlpha:
		mode = "LA"
	case mRGBA, mNRGBA:
		mode = "RGBA"
	case mCMYK:
		mode = "CMYK"
	}
	img, err := decodeImage(d)
	// The CCITT readers normalize fax runs and the grayscale reader also
	// applies PhotometricInterpretation. Preserve libtiff/Pillow's packed
	// mode-1 polarity: changing only tag262 must invert the resulting image.
	compression := d.firstVal(tCompression)
	if err == nil && mode == "1" && d.firstVal(tPhotometricInterpretation) == pBlackIsZero &&
		(compression == cCCITT || compression == cG3 || compression == cG4) {
		gray := img.(*image.Gray)
		for i := range gray.Pix {
			gray.Pix[i] ^= 0xff
		}
	}
	return img, mode, int(d.firstVal(274)), err
}

// TIFF's JPEG strips use the same libjpeg-compatible sample reconstruction as
// standalone source JPEGs; Go's standard IDCT is intentionally not substituted.
func decodePillowJPEG(r io.Reader) (image.Image, error) {
	pixels, err := jpeg.DecodeRaster(r)
	if err != nil {
		return nil, err
	}
	rect := image.Rect(0, 0, pixels.Width, pixels.Height)
	switch pixels.Mode {
	case "L":
		return &image.Gray{Pix: pixels.Pixels, Stride: pixels.Width, Rect: rect}, nil
	case "CMYK":
		return &image.CMYK{Pix: pixels.Pixels, Stride: pixels.Width * 4, Rect: rect}, nil
	default:
		out := image.NewNRGBA(rect)
		for i := 0; i < pixels.Width*pixels.Height; i++ {
			copy(out.Pix[i*4:i*4+3], pixels.Pixels[i*3:i*3+3])
			out.Pix[i*4+3] = 255
		}
		return out, nil
	}
}

func (d *decoder) applyScalarPredictor(xmin, ymin, xmax, ymax int) error {
	step := int(d.bpp / 8)
	samples := len(d.features[tBitsPerSample])
	rowSamples := (xmax - xmin) * samples
	rowBytes := rowSamples * step
	if step < 1 || rowBytes < 0 || (ymax-ymin) > len(d.buf)/max(rowBytes, 1) {
		return errNoPixels
	}
	for y := ymin; y < ymax; y++ {
		row := d.buf[(y-ymin)*rowBytes : (y-ymin+1)*rowBytes]
		if d.firstVal(tPredictor) == 3 {
			// TIFF Technical Note3: byte-plane differencing precedes the byte
			// shuffle, with most-significant planes first regardless of file endian.
			for i := samples; i < len(row); i++ {
				row[i] += row[i-samples]
			}
			planes := append([]byte(nil), row...)
			for i := 0; i < rowSamples; i++ {
				for b := 0; b < step; b++ {
					dest := b
					if d.byteOrder == binary.LittleEndian {
						dest = step - 1 - b
					}
					row[i*step+dest] = planes[b*rowSamples+i]
				}
			}
		} else {
			for i := samples; i < rowSamples; i++ {
				at, prev := i*step, (i-samples)*step
				switch step {
				case 4:
					d.byteOrder.PutUint32(row[at:], d.byteOrder.Uint32(row[at:])+d.byteOrder.Uint32(row[prev:]))
				case 8:
					d.byteOrder.PutUint64(row[at:], d.byteOrder.Uint64(row[at:])+d.byteOrder.Uint64(row[prev:]))
				default:
					return UnsupportedError("scalar predictor depth")
				}
			}
		}
	}
	return nil
}
