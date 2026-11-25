// Package imagelimit centralizes sanity checks for image dimensions and
// derived byte counts before decoders allocate memory.
package imagelimit

import (
	"fmt"
	"math"
)

const (
	// Image dimensions are checked against the fixed Pillow limit by the caller.
	// This leaf only checks integer overflow; it adds no different pixel quota.
	MaxPixels = math.MaxInt

	// MaxBytes protects derived counts from integer overflow only.
	MaxBytes = math.MaxInt
)

func Dimensions(width, height int) error {
	_, err := PixelCount(width, height)
	return err
}

func PixelCount(width, height int) (int, error) {
	if width <= 0 || height <= 0 {
		return 0, fmt.Errorf("invalid image dimensions %dx%d", width, height)
	}
	pixels, err := Mul(width, height)
	if err != nil {
		return 0, fmt.Errorf("invalid image dimensions %dx%d: %w", width, height, err)
	}
	if pixels > MaxPixels {
		return 0, fmt.Errorf("image dimensions %dx%d exceed pixel limit %d", width, height, MaxPixels)
	}
	return pixels, nil
}

func Bytes(parts ...int) (int, error) {
	n, err := Mul(parts...)
	if err != nil {
		return 0, err
	}
	if n > MaxBytes {
		return 0, fmt.Errorf("byte count %d exceeds limit %d", n, MaxBytes)
	}
	return n, nil
}

func Add(parts ...int) (int, error) {
	total, err := Sum(parts...)
	if err != nil {
		return 0, err
	}
	if total > MaxBytes {
		return 0, fmt.Errorf("byte count %d exceeds limit %d", total, MaxBytes)
	}
	return total, nil
}

func Sum(parts ...int) (int, error) {
	var total int
	for _, part := range parts {
		if part < 0 {
			return 0, fmt.Errorf("negative count %d", part)
		}
		if total > math.MaxInt-part {
			return 0, fmt.Errorf("count overflows int")
		}
		total += part
	}
	return total, nil
}

func Mul(parts ...int) (int, error) {
	total := 1
	for _, part := range parts {
		if part < 0 {
			return 0, fmt.Errorf("negative byte count %d", part)
		}
		if part != 0 && total > math.MaxInt/part {
			return 0, fmt.Errorf("byte count overflows int")
		}
		total *= part
	}
	return total, nil
}
