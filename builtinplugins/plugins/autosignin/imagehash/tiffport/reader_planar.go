package tiff

import (
	"image"

	jpegport "light-media/builtinplugins/plugins/autosignin/imagehash/jpegport"
)

func (d *decoder) uncompressed() bool {
	compression := d.firstVal(tCompression)
	return compression == 0 || compression == cNone
}

func (d *decoder) planarSamples() int {
	if d.firstVal(tPlanarConfiguration) != 2 {
		return 1
	}
	return max(1, len(d.features[tBitsPerSample]))
}

// Separate planes store all strips or tiles of one channel before the next.
// Undo each band's predictor before assembling a single interleaved block.
func (d *decoder) decodePlanarBlock(img image.Image, b tiffBlock, l blockLayout, index int, limit int64, interleaved *[]byte) error {
	samples := d.planarSamples()
	switch d.mode {
	case mRGB:
		samples = 3
	case mGrayAlpha:
		if d.uncompressed() {
			return UnsupportedError("separate raw grayscale-alpha planes")
		}
	case mRGBA, mNRGBA, mCMYK:
	default:
		return UnsupportedError("separate planes for this color model")
	}
	bytesPerSample := int(d.bpp / 8)
	size, ok := checkedMul3(b.width, b.height, samples*bytesPerSample)
	if !ok || size <= 0 || size > limit {
		return FormatError("planar block data size")
	}
	if int(size) > cap(*interleaved) {
		*interleaved = make([]byte, int(size))
	}
	buf := (*interleaved)[:int(size)]
	xmax, ymax := b.x+b.width, b.y+b.height
	// Pillow's raw planar tile selects a channel decoder ("R", "G", ...)
	// from the raw mode. It reads bytes even if the stored samples are 16-bit.
	rawBytePlane := bytesPerSample == 2 && d.uncompressed()
	planeStride := b.width * bytesPerSample
	if rawBytePlane && xmax <= d.config.Width {
		planeStride = b.width
	}
	planeSize := planeStride * b.height
	compression := d.firstVal(tCompression)
	for sample := 0; sample < samples; sample++ {
		segment := sample*l.across*l.down + index
		offset, err := l.offsets.value(segment)
		if err != nil {
			return err
		}
		n, err := l.counts.value(segment)
		if err != nil {
			return err
		}
		b.offset, b.n = int64(offset), int64(n)
		if d.mode == mGrayAlpha && compression == cJPEG {
			err = d.loadPlanarGrayAlphaJPEG(b, limit)
		} else {
			err = d.loadBlockData(b, int64(planeSize), compression)
		}
		if err != nil {
			return err
		}
		if len(d.buf) < planeSize {
			return errNoPixels
		}
		if compression != cJPEG && !d.uncompressed() {
			if err := d.applyPredictor(b.x, b.y, xmax, ymax, 1); err != nil {
				return err
			}
		}
		if d.mode == mGrayAlpha && sample == 1 {
			// Pillow's libtiff planar unpacker writes this plane into the
			// internal green byte, leaving the LA alpha byte zero. Read and
			// validate the plane before discarding its samples.
			clear(d.buf[:planeSize])
		}
		for y := 0; y < b.height; y++ {
			for x := 0; x < b.width; x++ {
				dst := ((y*b.width+x)*samples + sample) * bytesPerSample
				if rawBytePlane {
					value := d.buf[y*planeStride+x]
					buf[dst], buf[dst+1] = value, value
				} else {
					src := y*planeStride + x*bytesPerSample
					copy(buf[dst:dst+bytesPerSample], d.buf[src:src+bytesPerSample])
				}
			}
		}
	}
	block := *d
	block.buf = buf
	return block.decodePixels(img, b.x, b.y, xmax, ymax)
}

// The separated TIFF layout has one grayscale JPEG stream per plane.
func (d *decoder) loadPlanarGrayAlphaJPEG(block tiffBlock, limit int64) error {
	source := sectionJPEGSource(d.r, block.offset, block.n)
	if len(d.tables) != 0 {
		var err error
		source, err = d.prepJPEG(block.offset, block.n, limit, false, true)
		if err != nil {
			return err
		}
	}
	decoded, err := jpegport.DecodeRasterChecked(source(), func(width, height int) error {
		if width != block.width || height != block.height {
			return FormatError("JPEG dimensions do not match TIFF block")
		}
		return nil
	})
	if err != nil {
		return err
	}
	if decoded.Mode != "L" {
		return FormatError("planar grayscale-alpha JPEG requires one component per plane")
	}
	d.buf = decoded.Pixels
	return nil
}
