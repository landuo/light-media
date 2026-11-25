// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jpeg

// A block holds natural-order quantized DCT coefficients. int32 stores
// the decoder's progressive arithmetic; the installed SIMD IDCT explicitly
// narrows its input and intermediates to the source lane widths.
type block [blockSize]int32

const blockSize = 8 * 8
