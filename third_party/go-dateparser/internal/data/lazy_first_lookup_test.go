package data

import (
	"runtime"
	"sync"
	"testing"
)

func TestConcurrentFirstLocaleLookup(t *testing.T) {
	codes := []string{"en", "en-US", "en-GB", "fr", "de", "ja", "zh-Hans", "zh-Hant"}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 256; i++ {
		code := codes[i%len(codes)]
		wg.Go(func() {
			<-start
			value, ok := GetLocaleData(code)
			if !ok || value == nil || value.Name != code || value.DateOrder == "" || len(value.Translations) == 0 {
				t.Errorf("partially initialized locale %q", code)
				return
			}
			again, ok := GetLocaleData(code)
			if !ok || again != value {
				t.Errorf("locale identity changed for %q", code)
			}
		})
	}
	close(start)
	wg.Wait()
}

func TestLocaleCacheKeepsActivePointersAcrossEviction(t *testing.T) {
	code := "en-US"
	first, ok := GetLocaleData(code)
	if !ok {
		t.Fatal("missing fixture locale")
	}
	for _, other := range []string{"az-Cyrl", "af", "de", "es", "fr", "it", "ja", "nl", "pl", "ru", "zh-Hans", "zh-Hant"} {
		if loc, ok := GetLocaleData(other); !ok || loc.Name != other {
			t.Fatalf("wrong locale for %s", other)
		}
	}
	runtime.GC()
	again, _ := GetLocaleData(code)
	if first != again || first.Name != code || len(first.WordDictionary()) == 0 {
		t.Fatal("active caller lost its locale or dictionary")
	}
}

func TestLocaleFamiliesCoverGeneratedSlots(t *testing.T) {
	for code := range LocaleOrder {
		if _, ok := generatedLocaleSlot(code); !ok || !HasLocale(code) {
			t.Fatalf("missing language family for %s", code)
		}
	}
	if loc, ok := GetLocaleData("not-a-real-locale"); ok || loc != nil {
		t.Fatal("accepted an unknown locale")
	}
}
