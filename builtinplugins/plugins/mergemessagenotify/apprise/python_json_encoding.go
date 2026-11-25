package apprise

import (
	"bytes"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/encoding/unicode/utf32"
)

// pythonJSONEncoding follows json.detect_encoding for byte input. A UTF-32
// little-endian BOM shares its first two bytes with the UTF-16 BOM.
func pythonJSONEncoding(data []byte) ([]byte, error) {
	var decoder encoding.Encoding
	switch {
	case bytes.HasPrefix(data, []byte("\x00\x00\xfe\xff")), bytes.HasPrefix(data, []byte("\xff\xfe\x00\x00")):
		decoder = utf32.UTF32(utf32.BigEndian, utf32.UseBOM)
	case bytes.HasPrefix(data, []byte("\xfe\xff")), bytes.HasPrefix(data, []byte("\xff\xfe")):
		decoder = unicode.UTF16(unicode.BigEndian, unicode.UseBOM)
	case bytes.HasPrefix(data, []byte("\xef\xbb\xbf")):
		return data[3:], nil
	case len(data) >= 4 && data[0] == 0 && data[1] == 0:
		decoder = utf32.UTF32(utf32.BigEndian, utf32.IgnoreBOM)
	case len(data) >= 4 && data[1] == 0 && data[2] == 0 && data[3] == 0:
		decoder = utf32.UTF32(utf32.LittleEndian, utf32.IgnoreBOM)
	case (len(data) >= 4 || len(data) == 2) && data[0] == 0:
		decoder = unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM)
	case (len(data) >= 4 || len(data) == 2) && data[1] == 0:
		decoder = unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM)
	default:
		return data, nil
	}
	return decoder.NewDecoder().Bytes(data)
}
