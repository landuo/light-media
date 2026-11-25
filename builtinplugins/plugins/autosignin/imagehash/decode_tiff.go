package imagehash

import (
	"bytes"
	"light-media/builtinplugins/plugins/autosignin/imagehash/tiffport"
)

func decodeTIFF(encoded []byte) (Raster, error) {
	img, mode, orientation, err := tiff.DecodePillow(bytes.NewReader(encoded), checkImageSize)
	if err != nil {
		return Raster{}, err
	}
	if p, ok := img.(*tiff.Scalar); ok {
		return orientTIFF(Raster{Mode: mode, Width: p.Rect.Dx(), Height: p.Rect.Dy(), Pixels: p.Pix}, orientation), nil
	}
	r, err := fromImage(img, mode)
	if err != nil {
		return Raster{}, err
	}
	return orientTIFF(r, orientation), nil
}

// TiffImagePlugin.load_end applies ImageOps.exif_transpose before resize. Keep
// raw samples, palettes and scalar precision while mapping the eight TIFF axes.
func orientTIFF(r Raster, orientation int) Raster {
	if orientation < 2 || orientation > 8 {
		return r
	}
	out := r
	if orientation >= 5 {
		out.Width, out.Height = r.Height, r.Width
	}
	out.Pixels = make([]byte, len(r.Pixels))
	c := channels(r.Mode)
	for y := 0; y < r.Height; y++ {
		for x := 0; x < r.Width; x++ {
			dx, dy := x, y
			switch orientation {
			case 2:
				dx = r.Width - 1 - x
			case 3:
				dx, dy = r.Width-1-x, r.Height-1-y
			case 4:
				dy = r.Height - 1 - y
			case 5:
				dx, dy = y, x
			case 6:
				dx, dy = r.Height-1-y, x
			case 7:
				dx, dy = r.Height-1-y, r.Width-1-x
			case 8:
				dx, dy = y, r.Width-1-x
			}
			copy(out.Pixels[(dy*out.Width+dx)*c:], r.Pixels[(y*r.Width+x)*c:(y*r.Width+x+1)*c])
		}
	}
	return out
}
