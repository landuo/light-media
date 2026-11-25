package imagehash

import "encoding/binary"

// The fixed Pillow/OpenJPEG path returns the original RGB samples and carries
// ICC as metadata; it does not invoke ImageCms. The Go decoder otherwise applies
// an RGB matrix/TRC profile, changing the Tjupt hash. Preserve all codestream,
// palette and channel boxes while hiding only ICC colr metadata from that path.
func pillowJP2(data []byte) []byte {
	var result []byte
	var visit func(int, int, bool)
	visit = func(start, end int, inHeader bool) {
		for at := start; at+8 <= end; {
			n := uint64(binary.BigEndian.Uint32(data[at:]))
			header := 8
			if n == 1 {
				if at+16 > end {
					return
				}
				header = 16
				n = binary.BigEndian.Uint64(data[at+8:])
			} else if n == 0 {
				n = uint64(end - at)
			}
			if n < uint64(header) || n > uint64(end-at) {
				return
			}
			typ := string(data[at+4 : at+8])
			if typ == "jp2h" && !inHeader {
				visit(at+header, at+int(n), true)
			} else if inHeader && typ == "colr" && n > uint64(header) && data[at+header] == 2 {
				if result == nil {
					result = append([]byte(nil), data...)
				}
				copy(result[at+4:at+8], "free")
			}
			at += int(n)
		}
	}
	visit(0, len(data), false)
	if result != nil {
		return result
	}
	return data
}
