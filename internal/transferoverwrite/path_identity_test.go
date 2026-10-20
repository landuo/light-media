package transferoverwrite

import (
	"encoding/json"
	"light-media/internal/releasemeta"
	"os"
	"reflect"
	"testing"
)

func TestInstalledMetaInfoPathIdentity(t *testing.T) {
	b, e := os.ReadFile("testdata/path-identity-source.json")
	if e != nil {
		t.Fatal(e)
	}
	var doc struct {
		Cases []struct {
			Path, Mode string
			Words      []string
			Expected   Identity
		}
	}
	if e = json.Unmarshal(b, &doc); e != nil {
		t.Fatal(e)
	}
	for i, c := range doc.Cases {
		t.Run(c.Mode+"/"+c.Path+"/"+string(rune(i+32)), func(t *testing.T) {
			got, e := PathIdentity(c.Path, releasemeta.Options{SourceMode: c.Mode, CustomWords: c.Words, MediaExts: []string{".mkv", ".mp4", ".strm"}})
			if e != nil || !reflect.DeepEqual(got, c.Expected) {
				t.Fatalf("got=%+v err=%v want=%+v words=%v", got, e, c.Expected, c.Words)
			}
		})
	}
}
