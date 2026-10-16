package legacyanime

import "strings"

func initialize(filename string, options Options) (*state, string) {
	s := &state{elements: map[Category][]string{}, options: options}
	s.insert(FileName, filename)
	if enabled(options.FileExtension) {
		if at := strings.LastIndexByte(filename, '.'); at >= 0 {
			extension := filename[at+1:]
			if runeLen(extension) <= 4 && pyAlnum(extension) && findKeyword(normalize(extension), FileExtension) != nil {
				s.insert(FileExtension, extension)
				filename = filename[:at]
			}
		}
	}
	for _, ignored := range options.IgnoredStrings {
		filename = strings.ReplaceAll(filename, ignored, "")
	}
	return s, filename
}
