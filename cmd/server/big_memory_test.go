package main

import "testing"

func TestOriginalBigMemoryStartupBoolean(t *testing.T) {
	for _, s := range []string{"1", "true", "t", "YES", "y", "on", "0", "false", "f", "no", "n", "off", "", "maybe", " true "} {
		got, e := bigMemoryMode(func(k string) (string, bool) {
			if k != "BIG_MEMORY_MODE" {
				t.Fatal(k)
			}
			return s, true
		})
		if s == "" || s == "maybe" || s == " true " {
			if e == nil {
				t.Fatal(s)
			}
			continue
		}
		want := s == "1" || s == "true" || s == "t" || s == "YES" || s == "y" || s == "on"
		if e != nil || got != want {
			t.Fatal(s, got, e)
		}
	}
	b, e := bigMemoryMode(func(string) (string, bool) { return "", false })
	if b || e != nil {
		t.Fatal(b, e)
	}
}
