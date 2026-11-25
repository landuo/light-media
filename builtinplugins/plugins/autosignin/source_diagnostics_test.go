package autosignin

import (
	"encoding/hex"
	"os"
	"testing"

	"light-media/builtinplugins/plugins/mpserverstatus/encodingdetector"
)

func TestActualSourceCronDiagnostics(t *testing.T) {
	raw, err := os.ReadFile("reference/cron-diagnostics-source.json")
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Cases []struct {
			Expression string
			Error      Object
			Fields     []string
		}
	}
	if err = Decode(raw, &source); err != nil {
		t.Fatal(err)
	}
	if len(source.Cases) != 37 {
		t.Fatal(len(source.Cases))
	}
	for _, c := range source.Cases {
		t.Run(c.Expression, func(t *testing.T) {
			err := sourceCronCompileError(c.Expression)
			if c.Error != nil {
				if err == nil || err.Error() != c.Error["message"] {
					t.Fatalf("got %v want original %v", err, c.Error)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ParseCrontab(c.Expression, "UTC"); err != nil {
				t.Fatalf("source accepted expression rejected: %v", err)
			}
		})
	}
}
func TestActualSourceCharmapDiagnostics(t *testing.T) {
	raw, err := os.ReadFile("reference/charmap-diagnostics-source.json")
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Cases []struct {
			Codec  string
			RawHex string `json:"raw_hex"`
			Error  Object
		}
	}
	if err = Decode(raw, &source); err != nil {
		t.Fatal(err)
	}
	if len(source.Cases) == 0 {
		t.Fatal("empty original oracle")
	}
	for _, c := range source.Cases {
		t.Run(c.Codec+":"+c.RawHex, func(t *testing.T) {
			data, err := hex.DecodeString(c.RawHex)
			if err != nil {
				t.Fatal(err)
			}
			_, cause := encodingdetector.Decode(data, c.Codec, false)
			if cause == nil {
				t.Fatal("source invalid byte was accepted")
			}
			got := sourcePageDecodeError(data, c.Codec, cause)
			if got != c.Error["message"] {
				t.Fatalf("got %q want actual original %v", got, c.Error)
			}
		})
	}
}
