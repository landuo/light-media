package app

import (
	"context"
	"testing"
)

func TestActualOverwritePathParserUsesAuxiliarySuffixUnion(t *testing.T) {
	a, _, _ := importTestApp(t)
	ownedExtensionConfig(a, []string{".ownedvideo"}, []string{".ownedsub"}, []string{".ownedaudio"})
	for _, ext := range []string{".ownedvideo", ".ownedsub", ".ownedaudio"} {
		got, e := a.overwritePathIdentity(context.Background(), "/owned/Parent/Owned.S01-S03"+ext)
		if e != nil || got.Season != "S01" {
			t.Fatal("actual path parser did not use source file suffix union", ext, got, e)
		}
	}
}
