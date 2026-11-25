package autosignin

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"light-media/builtinplugins/plugins/mpserverstatus/encodingdetector"
)

//go:embed charmap-source-modules.json
var charmapSourceModules []byte
var charmapModulesOnce sync.Once
var charmapModules map[string]bool

func normalizedCodecName(name string) string {
	return strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(name))
}

// Only the source-confirmed one-byte charmap modules are handled here. Stateful
// codecs and unknown error kinds retain their actual Go error rather than a
// guessed Python reason. All charmap UnicodeDecodeError spans are one byte.
func sourcePageDecodeError(data []byte, encoding string, cause error) string {
	var codec *encodingdetector.CodecError
	if !errors.As(cause, &codec) || codec.Class != "UnicodeDecodeError" {
		return cause.Error()
	}
	charmapModulesOnce.Do(func() {
		var source struct{ Modules []struct{ Name string } }
		if err := json.Unmarshal(charmapSourceModules, &source); err != nil {
			panic(err)
		}
		charmapModules = map[string]bool{}
		for _, m := range source.Modules {
			charmapModules[normalizedCodecName(m.Name)] = true
		}
	})
	if !charmapModules[normalizedCodecName(codec.Encoding)] {
		return cause.Error()
	}
	for pos, b := range data {
		if _, err := encodingdetector.Decode([]byte{b}, encoding, false); err != nil {
			return fmt.Sprintf("'charmap' codec can't decode byte 0x%02x in position %d: character maps to <undefined>", b, pos)
		}
	}
	return cause.Error()
}
