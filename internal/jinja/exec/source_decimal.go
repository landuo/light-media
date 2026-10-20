package exec

import (
	"strings"
	"unicode"
)

func PythonDecimalASCII(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r > 127 && unicode.IsDigit(r) {
			found := false
			for _, v := range unicode.Digit.R16 {
				if uint32(r) >= uint32(v.Lo) && uint32(r) <= uint32(v.Hi) && (uint32(r)-uint32(v.Lo))%uint32(v.Stride) == 0 {
					b.WriteByte('0' + byte((uint32(r)-uint32(v.Lo))/uint32(v.Stride)%10))
					found = true
					break
				}
			}
			if !found {
				for _, v := range unicode.Digit.R32 {
					if uint32(r) >= v.Lo && uint32(r) <= v.Hi && (uint32(r)-v.Lo)%v.Stride == 0 {
						b.WriteByte('0' + byte((uint32(r)-v.Lo)/v.Stride%10))
						found = true
						break
					}
				}
			}
			if found {
				continue
			}
		}
		b.WriteRune(r)
	}
	return b.String()
}
