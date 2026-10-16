// Pure Go lane-by-lane translation of libjpeg-turbo 3.1.4.1's x86-64
// jidctint-avx2.asm DODCT (also the same arithmetic in jidctint-sse2.asm).
// Copyright 2009 Pierre Ossman <ossman@cendio.se> for Cendio AB
// Copyright (C) 2009, 2016, 2018, 2020, 2024-2025, D. R. Commander.
// Copyright (C) 2018, Matthias Räncker.
// Based on the x86 SIMD extension for IJG JPEG library
// Copyright (C) 1999-2006, MIYASAKA Masaru.
// Modified/translated to Go in 2026 for this independent compatibility port.
// Source notices and IJG/zlib licenses are retained in the LIBJPEG-* files.
//
// This deliberately simulates the installed NAS x86 SIMD path on every Go
// architecture: 16-bit low-word dequantization/addition, 32-bit multiply-add,
// signed saturation after both passes, and final signed-byte saturation.
// It is not the scalar C range-table path for oversized coefficient streams.
package jpeg

func descale(v int32, n uint) int32 { return (v + (1 << (n - 1))) >> n }
func word(v int32) int32            { return int32(int16(v)) }
func signedWord(v int32) int32      { return max(-32768, min(32767, v)) }

func idctOne(v [8]int32) [8]int32 {
	// pmaddwd distributes each rotator's multiplication so that its input
	// additions need not overflow a 16-bit lane. The two explicitly paddw
	// sums below must still wrap before conversion to signed 32-bit lanes.
	tmp2 := v[2]*4433 + v[6]*(4433-15137)
	tmp3 := v[2]*(4433+6270) + v[6]*4433
	tmp0 := word(v[0]+v[4]) << 13
	tmp1 := word(v[0]-v[4]) << 13
	tmp10, tmp13 := tmp0+tmp3, tmp0-tmp3
	tmp11, tmp12 := tmp1+tmp2, tmp1-tmp2
	z3, z4 := word(v[7]+v[3]), word(v[5]+v[1])
	z3m := z3*(9633-16069) + z4*9633
	z4m := z3*9633 + z4*(9633-3196)
	tmp0 = v[7]*(2446-7373) + v[1]*-7373 + z3m
	tmp1 = v[5]*(16819-20995) + v[3]*-20995 + z4m
	tmp2 = v[5]*-20995 + v[3]*(25172-20995) + z3m
	tmp3 = v[7]*-7373 + v[1]*(12299-7373) + z4m
	return [8]int32{tmp10 + tmp3, tmp11 + tmp2, tmp12 + tmp1, tmp13 + tmp0, tmp13 - tmp0, tmp12 - tmp1, tmp11 - tmp2, tmp10 - tmp3}
}
func idct(b *block, verticalACZero bool) {
	var work block
	for col := 0; col < 8; col++ {
		if verticalACZero {
			// The zero-column fast path tests original quantized ACs across the
			// entire block, then vpmullw + vpsllw; the left shift also wraps.
			dc := word(word(b[col]) << 2)
			for row := 0; row < 8; row++ {
				work[row*8+col] = dc
			}
			continue
		}
		var in [8]int32
		for row := 0; row < 8; row++ {
			in[row] = word(b[row*8+col])
		}
		out := idctOne(in)
		for row := 0; row < 8; row++ {
			work[row*8+col] = signedWord(descale(out[row], 11))
		}
	}
	for row := 0; row < 8; row++ {
		var in [8]int32
		copy(in[:], work[row*8:(row+1)*8])
		out := idctOne(in)
		for col := 0; col < 8; col++ {
			b[row*8+col] = signedWord(descale(out[col], 18))
		}
	}
}
func rangeSample(v int32) byte { return byte(max(-128, min(127, v)) + 128) }
