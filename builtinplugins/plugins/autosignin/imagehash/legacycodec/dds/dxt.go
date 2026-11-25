package dds

import (
	"encoding/binary"
	"fmt"
	"image"
	"image/color"

	"light-media/builtinplugins/plugins/autosignin/imagehash/legacycodec/internal/imagelimit"
)

func dxtBlockSize(fourCC string) (int, error) {
	switch fourCC {
	case "DXT1", "BC4":
		return 8, nil
	case "DXT3", "DXT5", "BC5", "BC5S", "BC6H", "BC6HS", "BC7":
		return 16, nil
	default:
		return 0, fmt.Errorf("unsupported DDS compression %q", fourCC)
	}
}

func Decode(data []byte, width, height int, fourCC string) (*image.NRGBA, error) {
	blockSize, err := dxtBlockSize(fourCC)
	if err != nil {
		return nil, err
	}
	blockColumns := (width + 3) / 4
	blockRows := (height + 3) / 4
	blockCount, err := imagelimit.Mul(blockColumns, blockRows)
	if err != nil {
		return nil, err
	}
	encodedBytes, err := imagelimit.Bytes(blockCount, blockSize)
	if err != nil {
		return nil, err
	}
	if len(data) < encodedBytes {
		return nil, fmt.Errorf("truncated DDS %s data", fourCC)
	}

	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for blockY := 0; blockY < blockRows; blockY++ {
		for blockX := 0; blockX < blockColumns; blockX++ {
			offset := (blockY*blockColumns + blockX) * blockSize
			block := data[offset : offset+blockSize]
			if fourCC != "DXT1" && fourCC != "DXT3" && fourCC != "DXT5" {
				pixels := decodeBCn(block, fourCC)
				for py := 0; py < 4; py++ {
					for px := 0; px < 4; px++ {
						x, y := blockX*4+px, blockY*4+py
						if x < width && y < height {
							img.SetNRGBA(x, y, pixels[py*4+px])
						}
					}
				}
				continue
			}
			colorBlock := block[blockSize-8:]
			color0 := binary.LittleEndian.Uint16(colorBlock[:2])
			color1 := binary.LittleEndian.Uint16(colorBlock[2:4])
			palette := dxtPalette(color0, color1, fourCC != "DXT1")
			indices := binary.LittleEndian.Uint32(colorBlock[4:8])
			var alphaBits uint64
			var alphaPalette [8]uint8
			if fourCC == "DXT3" {
				alphaBits = binary.LittleEndian.Uint64(block[:8])
			} else if fourCC == "DXT5" {
				alphaBits = binary.LittleEndian.Uint64(block[:8]) >> 16
				alphaPalette = dxt5AlphaPalette(block[0], block[1])
			}

			for pixelY := 0; pixelY < 4; pixelY++ {
				y := blockY*4 + pixelY
				if y >= height {
					break
				}
				for pixelX := 0; pixelX < 4; pixelX++ {
					x := blockX*4 + pixelX
					if x >= width {
						break
					}
					pixelIndex := uint(pixelY*4 + pixelX)
					paletteIndex := (indices >> (pixelIndex * 2)) & 0x3
					pixel := palette[paletteIndex]
					switch fourCC {
					case "DXT3":
						pixel.A = uint8((alphaBits>>(pixelIndex*4))&15) * 17
					case "DXT5":
						pixel.A = alphaPalette[(alphaBits>>(pixelIndex*3))&7]
					}
					img.SetNRGBA(x, y, pixel)
				}
			}
		}
	}
	return img, nil
}

func dxtPalette(color0, color1 uint16, forceFourColors bool) [4]color.NRGBA {
	first := decodeRGB565(color0)
	second := decodeRGB565(color1)
	palette := [4]color.NRGBA{first, second}
	// BC2/BC3 always use four colors, even with equal/reversed endpoints.
	if forceFourColors || color0 > color1 {
		palette[2] = interpolateDXTColor(first, second, 2, 1, 3)
		palette[3] = interpolateDXTColor(first, second, 1, 2, 3)
		return palette
	}
	palette[2] = interpolateDXTColor(first, second, 1, 1, 2)
	palette[3] = color.NRGBA{}
	return palette
}

func decodeRGB565(value uint16) color.NRGBA {
	return color.NRGBA{
		R: uint8(value>>11)<<3 | uint8(value>>11)>>2,
		G: uint8(value>>5&63)<<2 | uint8(value>>5&63)>>4,
		B: uint8(value&31)<<3 | uint8(value&31)>>2,
		A: 255,
	}
}

func interpolateDXTColor(first, second color.NRGBA, firstWeight, secondWeight, divisor uint16) color.NRGBA {
	interpolate := func(a, b uint8) uint8 {
		return uint8((uint16(a)*firstWeight + uint16(b)*secondWeight) / divisor)
	}
	return color.NRGBA{
		R: interpolate(first.R, second.R),
		G: interpolate(first.G, second.G),
		B: interpolate(first.B, second.B),
		A: 255,
	}
}

func dxt5AlphaPalette(alpha0, alpha1 uint8) [8]uint8 {
	palette := [8]uint8{alpha0, alpha1}
	// Use uint16 arithmetic: the weighted sum can exceed one byte.
	a0, a1 := uint16(alpha0), uint16(alpha1)
	if alpha0 > alpha1 {
		for i := uint16(2); i < 8; i++ {
			palette[i] = uint8(((8-i)*a0 + (i-1)*a1) / 7)
		}
	} else {
		for i := uint16(2); i < 6; i++ {
			palette[i] = uint8(((6-i)*a0 + (i-1)*a1) / 5)
		}
		palette[6], palette[7] = 0, 255
	}
	return palette
}
