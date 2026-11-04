package sourcecharset

import "bytes"

type cnSignature struct {
	Name string
	Mark []byte
}

// Exact source insertion order matters: UTF-32 must precede its UTF-16 prefix.
var cnSignatures = []cnSignature{
	{"utf_8", []byte{0xef, 0xbb, 0xbf}},
	{"utf_7", []byte{'+', '/', 'v', '8'}}, {"utf_7", []byte{'+', '/', 'v', '9'}}, {"utf_7", []byte{'+', '/', 'v', '+'}}, {"utf_7", []byte{'+', '/', 'v', '/'}},
	{"gb18030", []byte{0x84, 0x31, 0x95, 0x33}},
	{"utf_32", []byte{0, 0, 0xfe, 0xff}}, {"utf_32", []byte{0xff, 0xfe, 0, 0}},
	{"utf_16", []byte{0xfe, 0xff}}, {"utf_16", []byte{0xff, 0xfe}},
}

func cnIdentifySignature(data []byte) (Encoding, []byte) {
	for _, sig := range cnSignatures {
		if bytes.HasPrefix(data, sig.Mark) {
			return Encoding{Name: sig.Name, Present: true}, append([]byte(nil), sig.Mark...)
		}
	}
	return Encoding{}, []byte{}
}
func cnShouldStripSignature(name string) bool { return name != "utf_16" && name != "utf_32" }
