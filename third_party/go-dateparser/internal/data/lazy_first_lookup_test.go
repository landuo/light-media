package data

import (
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
