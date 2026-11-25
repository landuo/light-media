// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tiff

import (
	"bufio"
	"io"

	"golang.org/x/image/ccitt"
)

const (
	faxPass       = 7
	faxHorizontal = 8
)

// Vertical modes are coded as their displacement plus three.
var faxModes = buildMHTree([]mhCode{
	{3, "1"},
	{2, "010"}, {4, "011"},
	{1, "000010"}, {5, "000011"},
	{0, "0000010"}, {6, "0000011"},
	{faxPass, "0001"}, {faxHorizontal, "001"},
})

func decodeGroup3Mixed(r io.Reader, order ccitt.Order, width, height int, invert bool, limit int64) ([]byte, error) {
	stride := (width + 7) / 8
	size, ok := checkedMul3(stride, height, 1)
	if !ok || size > limit {
		return nil, FormatError("block data size too large")
	}
	input := mhBitReader{r: bufio.NewReader(r), order: order}
	pixels := make([]byte, size)
	var reference []byte
	for y := 0; y < height; y++ {
		if err := readGroup3EOL(&input); err != nil {
			return nil, err
		}
		mode, err := input.nextBit()
		if err != nil {
			return nil, err
		}
		row := pixels[y*stride : (y+1)*stride]
		if mode == 1 {
			err = decodeCCITTRLERow(&input, row, width, 1, 0)
		} else {
			err = decodeFax2DRow(&input, row, reference, width)
		}
		if err != nil {
			return nil, err
		}
		reference = row
	}
	if invert {
		for i := range pixels {
			pixels[i] = ^pixels[i]
		}
	}
	return pixels, nil
}

// T.4 permits zero fill before the eleven-zero EOL prefix.
func readGroup3EOL(input *mhBitReader) error {
	for zeros := 0; ; zeros++ {
		bit, err := input.nextBit()
		if err != nil {
			return err
		}
		if bit == 1 {
			if zeros < 11 {
				return FormatError("invalid CCITT Group 3 EOL")
			}
			return nil
		}
	}
}

func decodeFax2DRow(input *mhBitReader, row, reference []byte, width int) error {
	x := 0
	pen := byte(1)
	for atStart := true; x < width; atStart = false {
		mode, err := decodeMHCode(input, faxModes)
		if err != nil {
			return err
		}
		if mode == faxHorizontal {
			for range 2 {
				tree := mhBlackTree
				if pen == 1 {
					tree = mhWhiteTree
				}
				run, err := decodeMHRun(input, tree, width-x)
				if err != nil {
					return err
				}
				setMHRun(row, x, run, pen)
				x += run
				pen ^= 1
			}
			continue
		}
		next := faxReferenceEdge(reference, width, x, pen, atStart, mode == faxPass)
		if mode != faxPass {
			next += mode - 3
		}
		if next < x || next > width {
			return FormatError("CCITT Group 3 reference offset")
		}
		setMHRun(row, x, next-x, pen)
		x = next
		if mode != faxPass {
			pen ^= 1
		}
	}
	return nil
}

// As in golang.org/x/image/ccitt, b1 is the next reference transition to the
// opposite pen color. Pass mode uses b2, the transition immediately after b1.
func faxReferenceEdge(row []byte, width, x int, pen byte, atStart, pass bool) int {
	if row == nil {
		return width
	}
	sample := func(x int) byte { return (row[x/8] >> uint(7-x%8)) & 1 }
	if !atStart {
		for x < width && sample(x) != pen {
			x++
		}
	}
	for x < width && sample(x) == pen {
		x++
	}
	if pass {
		for x < width && sample(x) != pen {
			x++
		}
	}
	return x
}
