package jpeg

import "io"

type Raster struct {
	Mode          string
	Width, Height int
	Pixels        []byte
}

func clip(v int) byte {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return byte(v)
}

// jdcolor.c build_ycc_rgb_table uses separately rounded red/blue deltas and
// one combined green delta, not image/color's expanded 0x10101 luma formula.
func yccRGB(y, cb, cr byte) (byte, byte, byte) {
	c, b := int(cr)-128, int(cb)-128
	return clip(int(y) + ((91881*c + 32768) >> 16)), clip(int(y) + ((-22554*b - 46802*c + 32768) >> 16)), clip(int(y) + ((116130*b + 32768) >> 16))
}

func (d *decoder) componentSample(component, x, y int) byte {
	pix, stride := d.planes[component], d.strides[component]
	hr, vr := d.maxH/d.comp[component].h, d.maxV/d.comp[component].v
	width, height := (d.width+hr-1)/hr, (d.height+vr-1)/vr
	get := func(cx, cy int) int { return int(pix[max(0, min(cy, height-1))*stride+max(0, min(cx, width-1))]) }
	cx, cy := x/hr, y/vr
	switch {
	case hr == 1 && vr == 1:
		return byte(get(cx, cy))
	case hr == 2 && vr == 1 && width > 2:
		here := get(cx, cy)
		if x&1 == 0 {
			if cx == 0 {
				return byte(here)
			}
			return byte((here*3 + get(cx-1, cy) + 1) >> 2)
		}
		if cx == width-1 {
			return byte(here)
		}
		return byte((here*3 + get(cx+1, cy) + 2) >> 2)
	case hr == 1 && vr == 2:
		adj, bias := cy-1, 1
		if y&1 != 0 {
			adj, bias = cy+1, 2
		}
		return byte((get(cx, cy)*3 + get(cx, adj) + bias) >> 2)
	case hr == 2 && vr == 2 && width > 2:
		adj := cy - 1
		if y&1 != 0 {
			adj = cy + 1
		}
		sum := func(c int) int { return get(c, cy)*3 + get(c, adj) }
		here := sum(cx)
		if x&1 == 0 {
			if cx == 0 {
				return byte((here*4 + 8) >> 4)
			}
			return byte((here*3 + sum(cx-1) + 8) >> 4)
		}
		if cx == width-1 {
			return byte((here*4 + 7) >> 4)
		}
		return byte((here*3 + sum(cx+1) + 7) >> 4)
	default:
		return byte(get(cx, cy))
	}
}
func DecodeRaster(r io.Reader) (Raster, error) {
	var d decoder
	if e := d.decode(r); e != nil {
		return Raster{}, e
	}
	out := Raster{Width: d.width, Height: d.height}
	if d.nComp == 1 {
		out.Mode = "L"
		out.Pixels = make([]byte, d.width*d.height)
		for y := 0; y < d.height; y++ {
			copy(out.Pixels[y*d.width:], d.planes[0][y*d.strides[0]:y*d.strides[0]+d.width])
		}
		return out, nil
	}
	c := 3
	out.Mode = "RGB"
	if d.nComp == 4 {
		c = 4
		out.Mode = "CMYK"
	}
	out.Pixels = make([]byte, d.width*d.height*c)
	for y := 0; y < d.height; y++ {
		for x := 0; x < d.width; x++ {
			a, b, cc := d.componentSample(0, x, y), d.componentSample(1, x, y), d.componentSample(2, x, y)
			i := (y*d.width + x) * c
			if d.nComp == 3 {
				if d.isRGB() {
					out.Pixels[i], out.Pixels[i+1], out.Pixels[i+2] = a, b, cc
				} else {
					out.Pixels[i], out.Pixels[i+1], out.Pixels[i+2] = yccRGB(a, b, cc)
				}
			} else {
				if d.adobeTransformValid && d.adobeTransform != 0 {
					out.Pixels[i], out.Pixels[i+1], out.Pixels[i+2] = yccRGB(a, b, cc)
				} else {
					out.Pixels[i], out.Pixels[i+1], out.Pixels[i+2] = 255-a, 255-b, 255-cc
				}
				out.Pixels[i+3] = 255 - d.componentSample(3, x, y)
			}
		}
	}
	return out, nil
}
