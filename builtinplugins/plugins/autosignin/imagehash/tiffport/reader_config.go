// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tiff

import (
	"encoding/binary"
	"fmt"
	"image/color"
	"io"
)

func (d *decoder) readImageIFD(offset int64) error {
	p := make([]byte, 8)
	if _, err := d.r.ReadAt(p, 0); err != nil {
		if err == io.EOF {
			return io.ErrUnexpectedEOF
		}
		return err
	}
	switch string(p[:4]) {
	case leHeader:
		d.byteOrder = binary.LittleEndian
	case beHeader:
		d.byteOrder = binary.BigEndian
	default:
		return FormatError("malformed header")
	}
	if offset == 0 {
		offset = int64(d.byteOrder.Uint32(p[4:]))
	}
	count, err := safeReadAt(d.r, 2, offset)
	if err != nil {
		return err
	}
	// The first two bytes contain the number of entries (12 bytes each).
	numItems := int(d.byteOrder.Uint16(count))
	// All IFD entries are read in one chunk.
	entries, err := safeReadAt(d.r, uint64(ifdLen*numItems), offset+2)
	if err != nil {
		return err
	}
	return d.parseImageIFD(entries)
}

func (d *decoder) parseImageIFD(entries []byte) error {
	if err := d.collectImageIFD(entries); err != nil {
		return err
	}
	p := d.ifd[tPhotometricInterpretation]
	ycbcr := d.byteOrder.Uint16(p[2:4]) == dtShort && d.byteOrder.Uint32(p[4:8]) == 1 &&
		d.byteOrder.Uint16(p[8:10]) == pYCbCr
	if ycbcr {
		if err := d.initYCbCr(); err != nil {
			return err
		}
	}
	for i := 0; i < len(entries); i += ifdLen {
		p := entries[i : i+ifdLen]
		if ycbcr && d.byteOrder.Uint16(p[:2]) == tJPEGTables {
			continue
		}
		if _, err := d.parseIFD(p); err != nil {
			return err
		}
	}
	return nil
}

func (d *decoder) collectImageIFD(entries []byte) error {
	prevTag := -1
	for i := 0; i < len(entries); i += ifdLen {
		p := entries[i : i+ifdLen]
		tag := int(d.byteOrder.Uint16(p[:2]))
		if tag <= prevTag {
			return FormatError("tags are not sorted in ascending order")
		}
		var entry [ifdLen]byte
		copy(entry[:], p)
		d.ifd[tag] = entry
		prevTag = tag
	}
	return nil
}

func (d *decoder) initConfig() error {
	var err error
	if d.config.Width, err = d.firstIntVal(tImageWidth); err != nil {
		return err
	}
	if d.config.Height, err = d.firstIntVal(tImageLength); err != nil {
		return err
	}
	if _, ok := checkedMul3(d.config.Width, d.config.Height, maxBytesPerPixel); !ok {
		return FormatError("image too large")
	}
	if d.firstVal(tPhotometricInterpretation) == pYCbCr {
		if d.ycbcr != nil {
			return nil
		}
		return d.initYCbCr()
	}
	if err := d.initBitsPerSample(); err != nil {
		return err
	}
	return d.initColorModel()
}

func (d *decoder) initBitsPerSample() error {
	if _, ok := d.features[tBitsPerSample]; !ok {
		// Default is 1 per specification.
		d.features[tBitsPerSample] = []uint{1}
	}
	d.bpp = d.firstVal(tBitsPerSample)
	switch d.bpp {
	case 0:
		return FormatError("BitsPerSample must not be 0")
	case 1, 8, 16:
		return nil
	case 2, 4:
		photo := d.firstVal(tPhotometricInterpretation)
		if len(d.features[tBitsPerSample]) == 1 && (photo == pWhiteIsZero || photo == pBlackIsZero || photo == pPaletted) &&
			d.firstVal(tSampleFormat) <= 1 {
			return nil
		}
		return UnsupportedError("packed sample layout")
	case 32, 64:
		if d.firstVal(tSampleFormat) == 2 || d.firstVal(tSampleFormat) == 3 {
			return nil
		}
		return UnsupportedError(fmt.Sprintf("BitsPerSample of %v", d.bpp))
	default:
		return UnsupportedError(fmt.Sprintf("BitsPerSample of %v", d.bpp))
	}
}

func (d *decoder) initColorModel() error {
	if format := d.firstVal(tSampleFormat); format == 2 || format == 3 {
		if len(d.features[tBitsPerSample]) != 1 || (format == 2 && d.bpp > 32) || (format == 3 && d.bpp < 32) {
			return UnsupportedError("scalar sample layout")
		}
		if p := d.firstVal(tPhotometricInterpretation); p != pBlackIsZero && p != pWhiteIsZero {
			return UnsupportedError("scalar color model")
		}
		d.mode = mScalar
		d.config.ColorModel = color.GrayModel
		return nil
	}
	// Determine the image mode.
	switch d.firstVal(tPhotometricInterpretation) {
	case pRGB:
		return d.initRGB()
	case pPaletted:
		if d.bpp > 8 || len(d.palette) != 1<<d.bpp {
			return FormatError("invalid palette layout")
		}
		// Pillow names these raw modes in OPEN_INFO but registers only the
		// non-reversed sub-byte palette unpackers.
		if d.bpp < 8 && d.firstVal(tFillOrder) == 2 && d.uncompressed() {
			return UnsupportedError("reversed raw packed palette")
		}
		d.mode = mPaletted
		d.config.ColorModel = color.Palette(d.palette)
	case pWhiteIsZero, pBlackIsZero:
		bits := d.features[tBitsPerSample]
		extra := d.features[tExtraSamples]
		grayAlpha := d.firstVal(tPhotometricInterpretation) == pBlackIsZero && d.firstVal(tFillOrder) <= 1 &&
			d.bpp == 8 && len(extra) == 1 && extra[0] == 2
		compression := d.firstVal(tCompression)
		if grayAlpha && (compression == cJPEG || d.firstVal(tPlanarConfiguration) == 2) {
			samples := d.features[tSamplesPerPixel]
			if len(samples) != 1 || samples[0] != 2 || len(bits) > 2 || (len(bits) == 2 && bits[1] != 8) {
				return FormatError("invalid grayscale-alpha sample layout")
			}
			// A single BitsPerSample value applies to both bands.
			bits = []uint{8, 8}
			d.features[tBitsPerSample] = bits
		}
		if grayAlpha && len(bits) == 2 && bits[1] == 8 {
			if compression == cJPEGOld {
				return UnsupportedError("old JPEG luminance and alpha")
			}
			d.mode = mGrayAlpha
			d.config.ColorModel = color.NRGBAModel
			return nil
		}
		d.mode = mGray
		if d.firstVal(tPhotometricInterpretation) == pWhiteIsZero {
			d.mode = mGrayInvert
		}
		d.config.ColorModel = color.GrayModel
		if d.bpp == 16 {
			d.config.ColorModel = color.Gray16Model
		}
	case pCMYK:
		d.mode = mCMYK
		if d.bpp == 16 {
			return UnsupportedError(fmt.Sprintf("CMYK BitsPerSample of %v", d.bpp))
		}
		d.config.ColorModel = color.CMYKModel
	default:
		return UnsupportedError("color model")
	}
	return nil
}

func (d *decoder) initRGB() error {
	bits := uint(8)
	if d.bpp == 16 {
		bits = 16
	}
	for _, b := range d.features[tBitsPerSample] {
		if b != bits {
			return FormatError(fmt.Sprintf("wrong number of samples for %dbit RGB", bits))
		}
	}
	// RGB images normally have 3 samples per pixel.
	// If there are more, ExtraSamples (p. 31-32 of the spec)
	// gives their meaning (usually an alpha channel).
	samples := len(d.features[tBitsPerSample])
	extra := d.features[tExtraSamples]
	if samples == 4 && d.planarSamples() == 4 && len(extra) == 1 && extra[0] == 0 {
		samples = 3 // Pillow skips the unspecified extra band in separate planes.
	}
	switch samples {
	case 3:
		d.mode = mRGB
		d.config.ColorModel = color.RGBAModel
		if d.bpp == 16 {
			d.config.ColorModel = color.RGBA64Model
		}
	case 4:
		switch d.firstVal(tExtraSamples) {
		case 0, 1:
			d.setRGBAMode(true)
		case 2:
			d.setRGBAMode(false)
		default:
			return FormatError("wrong number of samples for RGB")
		}
	default:
		return FormatError("wrong number of samples for RGB")
	}
	return nil
}
