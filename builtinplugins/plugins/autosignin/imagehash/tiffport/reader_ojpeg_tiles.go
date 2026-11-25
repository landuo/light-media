package tiff

import (
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"

	jpegport "light-media/builtinplugins/plugins/autosignin/imagehash/jpegport"
)

// The old JPEG codec feeds all tiles to one restart-delimited decoder.
// Decode their vertical stack first, then place tiles in TIFF row-major order.
func (d *decoder) oldJPEGTiledSource(l blockLayout, components, horizontal, vertical int) (jpegSource, int, error) {
	if l.across <= 0 || l.down <= 0 || l.height <= 0 || l.across > 65535/l.height/l.down {
		return nil, 0, FormatError("old JPEG tile stack exceeds JPEG frame size")
	}
	height := l.height * l.down * l.across
	if d.oldJPEGInterchange() == nil {
		first, err := d.oldJPEGFirstBlock(l)
		if err != nil {
			return nil, 0, err
		}
		// A frame in the first tile may describe the entire tile stack.
		// Without that frame, libtiff only has the padded image height.
		frame, err := jpeg.DecodeConfig(first)
		if (err != nil && l.across != 1) || (err == nil && (frame.Width != l.width || frame.Height != height)) {
			return nil, 0, nil
		}
	}
	virtual := *d
	l.down *= l.across
	l.across, l.padding = 1, false
	if _, ok := checkedMul3(l.width, height, maxBytesPerPixel); !ok {
		return nil, 0, FormatError("old JPEG tile stack is too large")
	}
	virtual.config.Width, virtual.config.Height = l.width, height
	source, err := virtual.oldJPEGSource(l, components, horizontal, vertical)
	return source, height, err
}

func (d *decoder) decodeOldJPEGGrayTiles(dst *image.Gray, l blockLayout) (bool, error) {
	source, height, err := d.oldJPEGTiledSource(l, 1, 1, 1)
	if err != nil || source == nil {
		return false, err
	}
	pixels, err := jpegport.DecodeRasterChecked(source(), func(w, h int) error {
		if w != l.width || h != height {
			return FormatError("old JPEG dimensions do not match tile stack")
		}
		return nil
	})
	if err != nil {
		return true, err
	}
	if pixels.Mode != "L" {
		return true, FormatError("old JPEG grayscale tiles require one sample")
	}
	stack := &image.Gray{Pix: pixels.Pixels, Stride: l.width, Rect: image.Rect(0, 0, l.width, height)}
	for i := range l.across * l.down {
		x, y := i%l.across*l.width, i/l.across*l.height
		draw.Draw(dst, image.Rect(x, y, x+l.width, y+l.height), stack, image.Pt(0, i*l.height), draw.Src)
	}
	return true, nil
}

func (d *decoder) decodeOldJPEGColor(l ycbcrLayout) (image.Image, error) {
	horizontal, vertical := int(d.ycbcr.horizontal), int(d.ycbcr.vertical)
	blocks := blockLayout{width: l.width, height: l.height, across: l.across, down: l.down, offsets: l.offsets, counts: l.counts}
	width, height := d.config.Width, d.config.Height
	var source jpegSource
	var err error
	if l.tiled {
		width = l.width
		source, height, err = d.oldJPEGTiledSource(blocks, 3, horizontal, vertical)
	} else {
		source, err = d.oldJPEGSource(blocks, 3, horizontal, vertical)
	}
	if err != nil {
		return nil, err
	}
	if source == nil {
		return nil, UnsupportedError("missing old JPEG interchange stream")
	}
	samples, err := jpegport.DecodeTIFFYCbCr(source(), width, height, horizontal, vertical)
	if err != nil {
		return nil, err
	}
	img := image.NewRGBA(image.Rect(0, 0, d.config.Width, d.config.Height))
	if l.tiled {
		for i := range l.across * l.down {
			x, y := i%l.across*l.width, i/l.across*l.height
			rect := image.Rect(x, y, x+l.width, y+l.height).Intersect(img.Bounds())
			drawOldJPEGColor(img, rect, samples, i*l.height)
		}
	} else {
		drawOldJPEGColor(img, img.Bounds(), samples, 0)
	}
	return img, nil
}

func drawOldJPEGColor(dst *image.RGBA, rect image.Rectangle, samples *image.YCbCr, row int) {
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			s := samples.YCbCrAt(x-rect.Min.X, y-rect.Min.Y+row)
			dst.SetRGBA(x, y, oldJPEGColor(s.Y, s.Cb, s.Cr))
		}
	}
}

func oldJPEGColor(y, cb, cr byte) color.RGBA {
	luma, u, v := int(y), int(cb)-128, int(cr)-128
	// libtiff tif_color.c rounds the red and blue deltas separately and
	// rounds the combined green delta once.
	r := luma + ((91881*v + 32768) >> 16)
	g := luma + ((-22553*u - 46802*v + 32768) >> 16)
	b := luma + ((116130*u + 32768) >> 16)
	return color.RGBA{R: byte(min(max(r, 0), 255)), G: byte(min(max(g, 0), 255)), B: byte(min(max(b, 0), 255)), A: 255}
}
