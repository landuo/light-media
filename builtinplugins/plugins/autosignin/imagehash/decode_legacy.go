package imagehash

import "light-media/builtinplugins/plugins/autosignin/imagehash/legacycodec"

// decodeLegacy preserves each decoder's original Pillow mode so resize does
// not silently change palette/bitonal images from nearest to bicubic.
func decodeLegacy(encoded []byte) (Raster, bool, error) {
	r, ok, err := legacycodec.Decode(encoded, checkImageSize)
	if err != nil {
		return Raster{}, ok, err
	}
	return Raster{Mode: r.Mode, Width: r.Width, Height: r.Height, Pixels: r.Pixels, Palette: r.Palette}, ok, nil
}
