package htmlextract

// scriptEnd ports the consumed DATA_SCRIPT / ESC1 / ESC2 transition loop from
// pinned libxml2 HTMLparser.c htmlParseCharData. Its recovery deliberately keeps
// ESC1 after an unmatched '<'; the HTML5 tokenizer otherwise drops that state.
func scriptEnd(text string) int {
	mode := 4
	for i := 0; i < len(text); i++ {
		if text[i] == '-' && (mode == 5 || mode == 6) && i+2 < len(text) && text[i+1] == '-' && text[i+2] == '>' {
			mode = 4
		}
		if text[i] != '<' || i+1 >= len(text) {
			continue
		}
		j := i + 1
		if mode == 4 && text[j] == '!' {
			if j+2 < len(text) && text[j+1] == '-' && text[j+2] == '-' {
				mode = 5
			}
			continue
		}
		solidus := text[j] == '/'
		if solidus {
			j++
		}
		if !solidus && mode != 5 {
			continue
		}
		const tag = "script"
		k := 0
		for j < len(text) && k < len(tag) && text[j]|0x20 == tag[k] {
			j++
			k++
		}
		if k != len(tag) || j >= len(text) {
			continue
		}
		c := text[j]
		if c != '>' && c != '/' && c != ' ' && c != '\t' && c != '\n' && c != '\r' && c != '\f' {
			continue
		}
		if mode == 5 && !solidus {
			mode = 6
		} else if mode == 6 {
			mode = 5
		} else {
			return i
		}
	}
	return len(text)
}
