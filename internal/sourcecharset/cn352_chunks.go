package sourcecharset

import "strings"

func cnSliceBytes(data []byte, start, stop int) []byte {
	n := len(data)
	if start < 0 {
		start += n
	}
	if stop < 0 {
		stop += n
	}
	start = max(0, min(start, n))
	stop = max(0, min(stop, n))
	if stop < start {
		stop = start
	}
	return data[start:stop]
}
func cnSlicePoints(data []rune, start, stop int) []rune {
	start = max(0, min(start, len(data)))
	stop = max(start, min(stop, len(data)))
	return data[start:stop]
}
func cnContainsPoints(text, needle []rune) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(text); i++ {
		same := true
		for j, c := range needle {
			if text[i+j] != c {
				same = false
				break
			}
		}
		if same {
			return true
		}
	}
	return false
}

// Yield one original chunk at a time so the caller's threshold break stops
// future decodes exactly as the source generator does.
func cnCutChunks(data []byte, name string, offsets []int, size int, bom, strip bool, signature []byte, multibyte bool, decoded []rune, decodedPresent, deferred bool, yield func([]rune) (bool, error)) error {
	emit := func(points []rune) (bool, error) { return yield(points) }
	if len(decoded) > 0 && strings.HasPrefix(name, "iso2022_") {
		for _, i := range offsets {
			offset := i * len(decoded) / len(data)
			chunk := cnSlicePoints(decoded, offset, offset+size)
			if len(chunk) == 0 {
				break
			}
			more, err := emit(chunk)
			if err != nil || !more {
				return err
			}
		}
		return nil
	}
	if len(decoded) > 0 && !multibyte {
		for _, i := range offsets {
			chunk := cnSlicePoints(decoded, i, i+size)
			if len(chunk) == 0 {
				break
			}
			more, err := emit(chunk)
			if err != nil || !more {
				return err
			}
		}
		return nil
	}
	if deferred {
		base := data
		if strip {
			base = data[len(signature):]
		}
		for _, i := range offsets {
			chunk := cnSliceBytes(base, i, i+size)
			if len(chunk) == 0 {
				break
			}
			text, err := cnDecode(chunk, name, "strict")
			if err != nil {
				return err
			}
			points, err := cnCodepoints(text)
			if err != nil {
				return err
			}
			more, err := emit(points)
			if err != nil || !more {
				return err
			}
		}
		return nil
	}
	decode := func(start, end int) ([]rune, error) {
		chunk := cnSliceBytes(data, start, end)
		if bom && !strip {
			chunk = append(append([]byte(nil), signature...), chunk...)
		}
		mode := "strict"
		if multibyte {
			mode = "ignore"
		}
		text, err := cnDecode(chunk, name, mode)
		if err != nil {
			return nil, err
		}
		return cnCodepoints(text)
	}
	for _, i := range offsets {
		end := i + size
		if end > len(data)+8 {
			continue
		}
		chunk, err := decode(i, end)
		if err != nil {
			return err
		}
		if multibyte && i > 0 {
			check := min(size, 16)
			prefix := cnSlicePoints(chunk, 0, check)
			nearby := false
			if len(decoded) > 0 {
				expected := i * len(decoded) / len(data)
				nearby = cnContainsPoints(cnSlicePoints(decoded, max(0, expected-32768), min(len(decoded), expected+32768)), prefix)
			}
			if len(decoded) > 0 && !nearby && !cnContainsPoints(decoded, prefix) {
				for j := i; j > i-4; j-- {
					chunk, err = decode(j, end)
					if err != nil {
						return err
					}
					if cnContainsPoints(decoded, cnSlicePoints(chunk, 0, check)) {
						break
					}
				}
			}
		}
		more, err := emit(chunk)
		if err != nil || !more {
			return err
		}
	}
	return nil
}
