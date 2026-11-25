package avif

import (
	"encoding/binary"

	"github.com/gen2brain/gav1d/av1"
)

// libavif scales decoded tiles before assembling the image. For alpha it
// expands limited-range samples before invoking libyuv's box filter.
func (f *file) normalizePicture(pic *av1.Picture, width, height int, alpha bool) (*av1.Picture, error) {
	if width <= 0 || height <= 0 || width > 32768 || height > 32768 {
		return nil, ErrInvalid
	}
	limit := f.frameSizeLimit
	if limit == 0 {
		limit = av1.DefaultFrameSizeLimit
	}
	if limit > 0 && width > limit/height {
		return nil, av1.ErrFrameTooLarge
	}
	sameSize := pic.Width == width && pic.Height == height
	if sameSize && (!alpha || pic.FullRange) {
		return pic, nil
	}
	if !sameSize && (pic.Width > 16384 || pic.Height > 16384) {
		return nil, ErrUnsupported
	}
	if pic.BitDepth != 8 && pic.BitDepth != 10 && pic.BitDepth != 12 {
		return nil, ErrUnsupported
	}
	out := pic
	if !sameSize {
		out = newGrid(pic, gridInfo{w: width, h: height})
	}
	bytesPerSample := 1
	if pic.BitDepth > 8 {
		bytesPerSample = 2
	}
	sx, sy := subsampling(pic.Layout)
	for i, plane := range pic.Data {
		if plane == nil {
			continue
		}
		inHeight, outHeight := pic.Height, height
		if i > 0 {
			inHeight, outHeight = (inHeight+sy)>>sy, (outHeight+sy)>>sy
		}
		inWidth := pic.Width
		if i > 0 {
			inWidth = (inWidth + sx) >> sx
		}
		in := scalePlane{data: plane, stride: pic.Stride[i], width: inWidth, height: inHeight, sampleBytes: bytesPerSample}
		dst := scalePlane{data: out.Data[i], stride: out.Stride[i], width: out.Stride[i] / bytesPerSample, height: outHeight, sampleBytes: bytesPerSample}
		if sameSize {
			dst.width = inWidth
		}
		if alpha && i == 0 && !pic.FullRange {
			shift := pic.BitDepth - 8
			in.alphaBias, in.alphaSpan, in.alphaMax = 16<<shift, 219<<shift, (1<<pic.BitDepth)-1
		}
		resizePlane(in, dst)
	}
	out.FullRange = pic.FullRange || alpha
	if !sameSize {
		pic.Release()
	}
	return out, nil
}

type scalePlane struct {
	data                  []byte
	stride, width, height int
	sampleBytes           int
	alphaBias, alphaSpan  int
	alphaMax              int
}

func (p scalePlane) sample(x, y int) int {
	i := y*p.stride + x*p.sampleBytes
	value := int(p.data[i])
	if p.sampleBytes == 2 {
		value = int(binary.NativeEndian.Uint16(p.data[i:]))
	}
	if p.alphaSpan != 0 {
		value = min(p.alphaSpan, max(0, value-p.alphaBias))
		value = (value*p.alphaMax + p.alphaSpan/2) / p.alphaSpan
	}
	return value
}

func (p scalePlane) set(x, y, value int) {
	i := y*p.stride + x*p.sampleBytes
	if p.sampleBytes == 1 {
		p.data[i] = byte(value)
	} else {
		binary.NativeEndian.PutUint16(p.data[i:], uint16(value))
	}
}

type scaleFilter uint8

const (
	scalePoint scaleFilter = iota
	scaleLinear
	scaleBilinear
	scaleBox
)

// ScaleFilterReduce and the ratio dispatch from the installed libyuv.
func resizePlane(in, dst scalePlane) {
	if in.width == dst.width && in.height == dst.height {
		for y := range dst.height {
			for x := range dst.width {
				dst.set(x, y, in.sample(x, y))
			}
		}
		return
	}
	filter := scaleBox
	if 2*dst.width >= in.width || 2*dst.height >= in.height {
		filter = scaleBilinear
		if in.height == 1 || dst.height == in.height || 3*dst.height == in.height {
			filter = scaleLinear
		}
		if in.width == 1 || (filter == scaleLinear && (dst.width == in.width || 3*dst.width == in.width)) {
			filter = scalePoint
		}
	}

	switch {
	case in.width == dst.width && filter != scaleBox:
		scaleVertical(in, dst, filter)
	case 4*dst.width == 3*in.width && 4*dst.height == 3*in.height:
		scaleThreeFourths(in, dst)
	case 2*dst.width == in.width && 2*dst.height == in.height:
		scaleIntegerBox(in, dst, 2)
	case 8*dst.width == 3*in.width && 8*dst.height == 3*in.height:
		scaleThreeEighths(in, dst)
	case 4*dst.width == in.width && 4*dst.height == in.height:
		scaleIntegerBox(in, dst, 4)
	case filter == scaleBox:
		scaleAnyBox(in, dst)
	case (dst.width+1)/2 == in.width && filter == scaleLinear:
		scaleUpTwo(in, dst, false)
	case (dst.width+1)/2 == in.width && (dst.height+1)/2 == in.height && filter == scaleBilinear:
		scaleUpTwo(in, dst, true)
	default:
		scaleInterpolated(in, dst, filter)
	}
}

const scaleOne = 1 << 16

func scaleSlope(src, dst int, filtered bool) (start, step int) {
	if filtered && dst > src {
		if src > 1 {
			step = ((src-1)*scaleOne - 1) / (dst - 1)
		}
		return 0, step
	}
	step = src * scaleOne / dst
	start = step / 2
	if filtered {
		start -= scaleOne / 2
	}
	return start, step
}

func blendScale(a, b, fraction, bits int) int {
	return a + ((fraction*(b-a) + (1 << (bits - 1))) >> bits)
}

func scaleVertical(in, dst scalePlane, filter scaleFilter) {
	blend := filter == scaleBilinear
	y0, dy := scaleSlope(in.height, dst.height, blend || dst.height <= in.height)
	limit := (in.height - 1) * scaleOne
	if blend {
		limit = max(0, limit-1)
	}
	for y := range dst.height {
		p := min(limit, y0+y*dy)
		row, fraction := p/scaleOne, (p>>8)&255
		for x := range dst.width {
			v := in.sample(x, row)
			if blend {
				v = blendScale(v, in.sample(x, min(in.height-1, row+1)), fraction, 8)
			}
			dst.set(x, y, v)
		}
	}
}

func scaleInterpolated(in, dst scalePlane, filter scaleFilter) {
	x0, dx := scaleSlope(in.width, dst.width, filter != scalePoint)
	y0, dy := scaleSlope(in.height, dst.height, filter == scaleBilinear)
	hBits := 16
	if in.sampleBytes == 1 {
		hBits = 7
	}
	for y := range dst.height {
		py := min((in.height-1)*scaleOne, y0+y*dy)
		top := py / scaleOne
		bottom := min(in.height-1, top+1)
		fy := (py >> 8) & 255
		for x := range dst.width {
			px := x0 + x*dx
			left := min(in.width-1, px/scaleOne)
			right := min(in.width-1, left+1)
			fx := (px & (scaleOne - 1)) >> (16 - hBits)
			a := in.sample(left, top)
			switch filter {
			case scaleLinear:
				a = blendScale(a, in.sample(right, top), fx, hBits)
			case scaleBilinear:
				b, c, d := in.sample(right, top), in.sample(left, bottom), in.sample(right, bottom)
				// The original rounds after each axis. Upscaling processes
				// rows horizontally first, downscaling vertically first.
				if dst.height > in.height {
					a = blendScale(blendScale(a, b, fx, hBits), blendScale(c, d, fx, hBits), fy, 8)
				} else {
					a = blendScale(blendScale(a, c, fy, 8), blendScale(b, d, fy, 8), fx, hBits)
				}
			}
			dst.set(x, y, a)
		}
	}
}

func scaleUpTwo(in, dst scalePlane, vertical bool) {
	for y := range dst.height {
		y0, y1 := (in.height-1)/2, (in.height-1)/2
		if vertical {
			y0 = y / 2
			y1 = min(in.height-1, max(0, y0+2*(y%2)-1))
		} else if dst.height > 1 {
			y0 = (scaleOne/2 - 1 + y*((in.height-1)*scaleOne/(dst.height-1))) / scaleOne
			y1 = y0
		}
		for x := range dst.width {
			x0 := x / 2
			x1 := min(in.width-1, max(0, x0+2*(x%2)-1))
			if x == dst.width-1 {
				x1 = x0
			}
			a := 3*in.sample(x0, y0) + in.sample(x1, y0)
			b := 3*in.sample(x0, y1) + in.sample(x1, y1)
			dst.set(x, y, (3*a+b+8)>>4)
		}
	}
}

func scaleIntegerBox(in, dst scalePlane, ratio int) {
	area := ratio * ratio
	for y := range dst.height {
		for x := range dst.width {
			sum := sumScaleBox(in, ratio*x, ratio*y, ratio, ratio, false)
			dst.set(x, y, (sum+area/2)/area)
		}
	}
}

func scaleThreeFourths(in, dst scalePlane) {
	for y := range dst.height {
		y0 := y/3*4 + y%3
		y1, fy := y0+1, 1
		if y%3 == 1 {
			fy = 2
		} else if y%3 == 2 {
			y0, y1 = y1, y0
		}
		for x := range dst.width {
			x0 := x/3*4 + x%3
			fx := x%3 + 1
			var value int
			if in.sampleBytes == 1 && x < dst.width-dst.width%24 {
				// Installed AVX2 rounds vertically before applying horizontal
				// weights. A 3:1 vertical phase is two rounded averages.
				column := func(x int) int {
					top, bottom := in.sample(x, y0), in.sample(x, y1)
					v := (top + bottom + 1) >> 1
					if fy == 1 {
						v = (v + top + 1) >> 1
					}
					return v
				}
				value = blendScale(column(x0), column(x0+1), fx, 2)
			} else {
				top := blendScale(in.sample(x0, y0), in.sample(x0+1, y0), fx, 2)
				bottom := blendScale(in.sample(x0, y1), in.sample(x0+1, y1), fx, 2)
				value = blendScale(top, bottom, fy, 2)
			}
			dst.set(x, y, value)
		}
	}
}

func scaleThreeEighths(in, dst scalePlane) {
	for y := range dst.height {
		y0, h := y/3*8+y%3*3, 3
		if y%3 == 2 {
			h = 2
		}
		for x := range dst.width {
			x0, w := x/3*8+x%3*3, 3
			if x%3 == 2 {
				w = 2
			}
			sum, area := 0, w*h
			if h == 2 && in.sampleBytes == 1 && x < dst.width-dst.width%12 {
				for i := range w {
					sum += (in.sample(x0+i, y0) + in.sample(x0+i, y0+1) + 1) >> 1
				}
				area = w
			} else {
				sum = sumScaleBox(in, x0, y0, w, h, false)
			}
			dst.set(x, y, sum*(scaleOne/area)>>16)
		}
	}
}

func scaleAnyBox(in, dst scalePlane) {
	dx, dy := in.width*scaleOne/dst.width, in.height*scaleOne/dst.height
	for y := range dst.height {
		y0 := y * dy / scaleOne
		h := max(1, (y+1)*dy/scaleOne-y0)
		for x := range dst.width {
			x0 := x * dx / scaleOne
			w := max(1, (x+1)*dx/scaleOne-x0)
			sum := sumScaleBox(in, x0, y0, w, h, true)
			dst.set(x, y, sum*(scaleOne/(w*h))>>16)
		}
	}
}

func sumScaleBox(in scalePlane, x, y, w, h int, narrowColumns bool) int {
	sum := 0
	for col := x; col < x+w; col++ {
		column := 0
		for row := y; row < y+h; row++ {
			column += in.sample(col, row)
		}
		if narrowColumns && in.sampleBytes == 1 {
			if col < in.width-in.width%32 {
				column = min(column, 65535)
			} else {
				column &= 65535
			}
		}
		sum += column
	}
	return sum
}
