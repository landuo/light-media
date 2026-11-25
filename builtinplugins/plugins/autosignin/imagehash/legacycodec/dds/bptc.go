// BC4/BC5/BC6H/BC7 decoding is translated from Pillow 12.2.0 BcnDecode.c.
// The original file is dedicated to the public domain under CC0.
package dds

import (
	"encoding/binary"
	"image/color"
	"math"
)

func blockBits(src []byte, bit, count int) int {
	v := 0
	for i := 0; i < count; i++ {
		v |= int(src[(bit+i)/8]>>uint((bit+i)%8)&1) << uint(i)
	}
	return v
}
func blockWeights(n int) []int {
	if n == 2 {
		return bc7_weights2
	}
	if n == 3 {
		return bc7_weights3
	}
	return bc7_weights4
}
func blockSubset(ns, partition, pixel int) int {
	if ns == 2 {
		return int(bc7_si2[partition] >> uint(pixel) & 1)
	}
	if ns == 3 {
		return int(bc7_si3[partition] >> uint(pixel*2) & 3)
	}
	return 0
}
func blockExpand(v, bits int) int { v = int(byte(v << uint(8-bits))); return v | v>>uint(bits) }
func blockAlpha(src []byte, signed bool) [16]byte {
	a0, a1 := src[0], src[1]
	if signed {
		a0 = byte(int(int8(a0)) + 128)
		a1 = byte(int(int8(a1)) + 128)
	}
	palette := dxt5AlphaPalette(a0, a1)
	indices := binary.LittleEndian.Uint64(src) >> 16
	var out [16]byte
	for i := range out {
		out[i] = palette[indices>>uint(i*3)&7]
	}
	return out
}
func decodeBCn(src []byte, format string) [16]color.NRGBA {
	var out [16]color.NRGBA
	switch format {
	case "BC4":
		p := blockAlpha(src, false)
		for i := range out {
			out[i] = color.NRGBA{R: p[i], G: p[i], B: p[i], A: 255}
		}
	case "BC5", "BC5S":
		signed := format == "BC5S"
		r, g := blockAlpha(src, signed), blockAlpha(src[8:], signed)
		blue := byte(0)
		if signed {
			blue = 128
		}
		for i := range out {
			out[i] = color.NRGBA{R: r[i], G: g[i], B: blue, A: 255}
		}
	case "BC6H", "BC6HS":
		out = decodeBC6(src, format == "BC6HS")
	case "BC7":
		out = decodeBC7(src)
	}
	return out
}
func decodeBC7(src []byte) [16]color.NRGBA {
	var out [16]color.NRGBA
	if src[0] == 0 {
		for i := range out {
			out[i].A = 255
		}
		return out
	}
	mode := 0
	for src[0]&(1<<uint(mode)) == 0 {
		mode++
	}
	info := bc7_modes[mode]
	ns, pb, rb, isb, cb, ab, epb, spb, ib, ib2 := info[0], info[1], info[2], info[3], info[4], info[5], info[6], info[7], info[8], info[9]
	cw := blockWeights(ib)
	aw := cw
	if ab != 0 && ib2 != 0 {
		aw = blockWeights(ib2)
	}
	bit := mode + 1
	load := func(n int) int { v := blockBits(src, bit, n); bit += n; return v }
	partition, rotation, indexSel := load(pb), load(rb), load(isb)
	numep := ns * 2
	var endpoints [6][4]int
	for ch := 0; ch < 3; ch++ {
		for i := 0; i < numep; i++ {
			endpoints[i][ch] = load(cb)
		}
	}
	for i := 0; i < numep; i++ {
		endpoints[i][3] = 255
		if ab != 0 {
			endpoints[i][3] = load(ab)
		}
	}
	if epb != 0 {
		cb++
		if ab != 0 {
			ab++
		}
		for i := 0; i < numep; i++ {
			v := load(1)
			for ch := 0; ch < 4; ch++ {
				if ch == 3 && ab == 0 {
					continue
				}
				endpoints[i][ch] = endpoints[i][ch]<<1 | v
			}
		}
	}
	if spb != 0 {
		cb++
		if ab != 0 {
			ab++
		}
		for i := 0; i < numep; i += 2 {
			v := load(1)
			for j := 0; j < 2; j++ {
				for ch := 0; ch < 4; ch++ {
					if ch == 3 && ab == 0 {
						continue
					}
					endpoints[i+j][ch] = endpoints[i+j][ch]<<1 | v
				}
			}
		}
	}
	for i := 0; i < numep; i++ {
		for ch := 0; ch < 3; ch++ {
			endpoints[i][ch] = blockExpand(endpoints[i][ch], cb)
		}
		if ab != 0 {
			endpoints[i][3] = blockExpand(endpoints[i][3], ab)
		}
	}
	colorBit, alphaBit := bit, bit+16*ib-ns
	for i := 0; i < 16; i++ {
		subset := blockSubset(ns, partition, i) * 2
		n := ib
		if i == 0 || ns == 2 && i == bc7_ai0[partition] || ns == 3 && (i == bc7_ai1[partition] || i == bc7_ai2[partition]) {
			n--
		}
		index := blockBits(src, colorBit, n)
		colorBit += n
		cw0, aw0 := cw[index], cw[index]
		if ab != 0 && ib2 != 0 {
			n = ib2
			if i == 0 {
				n--
			}
			ai := blockBits(src, alphaBit, n)
			alphaBit += n
			if indexSel != 0 {
				cw0, aw0 = aw[ai], cw[index]
			} else {
				cw0, aw0 = cw[index], aw[ai]
			}
		}
		var pixel [4]byte
		for ch := 0; ch < 4; ch++ {
			weight := cw0
			if ch == 3 {
				weight = aw0
			}
			pixel[ch] = byte(((64-weight)*endpoints[subset][ch] + weight*endpoints[subset+1][ch] + 32) >> 6)
		}
		if rotation != 0 {
			pixel[rotation-1], pixel[3] = pixel[3], pixel[rotation-1]
		}
		out[i] = color.NRGBA{R: pixel[0], G: pixel[1], B: pixel[2], A: pixel[3]}
	}
	return out
}
func bc6Sign(v uint16, precision int) uint16 {
	x := int(v)
	if x&(1<<uint(precision-1)) != 0 {
		x |= -(1 << uint(precision))
	}
	return uint16(x)
}
func bc6Unquantize(v uint16, precision int, signed bool) int {
	x := int(v)
	if !signed {
		if precision >= 15 {
			return x
		}
		if x == 0 {
			return 0
		}
		if x == (1<<uint(precision))-1 {
			return 65535
		}
		return ((x << 15) + 16384) >> uint(precision-1)
	}
	x = int(int16(v))
	if precision >= 16 {
		return x
	}
	negative := x < 0
	if negative {
		x = -x
	}
	if x != 0 {
		if x >= (1<<uint(precision-1))-1 {
			x = 32767
		} else {
			x = ((x << 15) + 16384) >> uint(precision-1)
		}
	}
	if negative {
		return -x
	}
	return x
}
func bc6Half(h uint16) float32 {
	// Preserve the fixed source's float32 multiplication, infinity handling and
	// sign bit, including its half subnormal conversion.
	bits := uint32(h&0x7fff) << 13
	v := float32(math.Float32frombits(bits) * math.Float32frombits(0x77800000))
	bits = math.Float32bits(v)
	if v >= math.Float32frombits(0x47800000) {
		bits |= 255 << 23
	}
	bits |= uint32(h&0x8000) << 16
	return math.Float32frombits(bits)
}
func bc6Final(v int, signed bool) byte {
	var h uint16
	if signed {
		if v < 0 {
			h = 0x8000 | uint16((-v)*31/32)
		} else {
			h = uint16(v * 31 / 32)
		}
	} else {
		h = uint16(v * 31 / 64)
	}
	value := bc6Half(h)
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 255
	}
	if math.IsNaN(float64(value)) {
		return 0
	}
	return byte(float32(value * 255))
}
func decodeBC6(src []byte, signed bool) [16]color.NRGBA {
	var out [16]color.NRGBA
	bit, epbits, ib, mode := 5, 75, 3, int(src[0]&31)
	if mode&3 == 0 || mode&3 == 1 {
		mode &= 3
		bit = 2
	} else if mode&3 == 2 {
		mode = 2 + (mode >> 2)
		epbits = 72
	} else {
		mode = 10 + (mode >> 2)
		epbits = 60
		ib = 4
	}
	if mode >= 14 {
		return out
	}
	info := bc6_modes[mode]
	ns, tr, pb, epb, rb, gb, bb := info[0], info[1], info[2], info[3], info[4], info[5], info[6]
	numep := 6
	if ns == 2 {
		numep = 12
	}
	weights := blockWeights(ib)
	var endpoints [12]uint16
	var values [12]int
	for i := 0; i < epbits; i++ {
		di := bc6_bit_packings[mode][i]
		endpoints[di>>4] |= uint16(blockBits(src, bit+i, 1)) << uint(di&15)
	}
	bit += epbits
	partition := blockBits(src, bit, pb)
	bit += pb
	mask := uint16((1 << uint(epb)) - 1)
	if signed {
		for i := 0; i < 3; i++ {
			endpoints[i] = bc6Sign(endpoints[i], epb)
		}
	}
	if signed || tr != 0 {
		for i := 3; i < numep; i += 3 {
			endpoints[i] = bc6Sign(endpoints[i], rb)
			endpoints[i+1] = bc6Sign(endpoints[i+1], gb)
			endpoints[i+2] = bc6Sign(endpoints[i+2], bb)
		}
	}
	if tr != 0 {
		for i := 3; i < numep; i++ {
			endpoints[i] = (endpoints[i] + endpoints[i%3]) & mask
		}
	}
	for i := 0; i < numep; i++ {
		values[i] = bc6Unquantize(endpoints[i], epb, signed)
	}
	for i := 0; i < 16; i++ {
		subset := blockSubset(ns, partition, i) * 6
		n := ib
		if i == 0 || ns == 2 && i == bc7_ai0[partition] {
			n--
		}
		index := blockBits(src, bit, n)
		bit += n
		weight := weights[index]
		var pixel [3]byte
		for ch := 0; ch < 3; ch++ {
			v := (values[subset+ch]*(64-weight) + values[subset+3+ch]*weight) >> 6
			pixel[ch] = bc6Final(v, signed)
		}
		out[i] = color.NRGBA{R: pixel[0], G: pixel[1], B: pixel[2], A: 255}
	}
	return out
}
