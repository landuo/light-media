package app

import (
	"fmt"
	sourceexec "light-media/internal/jinja/exec"
	"math/big"
	"strings"
	"unicode"
)

type originalBrowserNaturalPart struct {
	Text   string
	Number *big.Int
}

// CPython3.12.13 / Unicode15.0 digit-but-not-decimal ranges. The pinned natural
// key calls int on an all-digit nonsplit chunk, which rejects these characters.
var originalBrowserNonDecimalDigits = [][2]rune{
	{0xb2, 0xb3},
	{0xb9, 0xb9},
	{0x1369, 0x1371},
	{0x19da, 0x19da},
	{0x2070, 0x2070},
	{0x2074, 0x2079},
	{0x2080, 0x2089},
	{0x2460, 0x2468},
	{0x2474, 0x247c},
	{0x2488, 0x2490},
	{0x24ea, 0x24ea},
	{0x24f5, 0x24fd},
	{0x24ff, 0x24ff},
	{0x2776, 0x277e},
	{0x2780, 0x2788},
	{0x278a, 0x2792},
	{0x10a40, 0x10a43},
	{0x10e60, 0x10e68},
	{0x11052, 0x1105a},
	{0x1f100, 0x1f10a},
}

func originalBrowserNaturalKey(name string) ([]originalBrowserNaturalPart, error) {
	parts := []originalBrowserNaturalPart{}
	var chunk strings.Builder
	numeric := false
	flush := func() error {
		text := chunk.String()
		chunk.Reset()
		if numeric {
			if len([]rune(text)) > 4300 {
				return fmt.Errorf("source natural-sort integer limit")
			}
			number, ok := new(big.Int).SetString(sourceexec.PythonDecimalASCII(text), 10)
			if !ok {
				return fmt.Errorf("invalid natural-sort number")
			}
			parts = append(parts, originalBrowserNaturalPart{Number: number})
			return nil
		}
		allExtra := text != ""
		for _, r := range text {
			found := false
			for _, v := range originalBrowserNonDecimalDigits {
				if r >= v[0] && r <= v[1] {
					found = true
					break
				}
			}
			if !found {
				allExtra = false
				break
			}
		}
		if allExtra {
			return fmt.Errorf("source natural-sort nondecimal digit")
		}
		parts = append(parts, originalBrowserNaturalPart{Text: sourceexec.PythonCase(text, "lower")})
		return nil
	}
	for _, r := range name {
		digit := unicode.IsDigit(r)
		if digit != numeric {
			if err := flush(); err != nil {
				return nil, err
			}
			numeric = digit
		}
		chunk.WriteRune(r)
	}
	if err := flush(); err != nil {
		return nil, err
	}
	if numeric {
		parts = append(parts, originalBrowserNaturalPart{})
	}
	return parts, nil
}
func originalBrowserNaturalCompare(a, b []originalBrowserNaturalPart) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		var n int
		if a[i].Number != nil && b[i].Number != nil {
			n = a[i].Number.Cmp(b[i].Number)
		} else {
			n = strings.Compare(a[i].Text, b[i].Text)
		}
		if n != 0 {
			return n
		}
	}
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return 0
}
