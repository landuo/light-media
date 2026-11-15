package historywords

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"runtime"
	"slices"
	"testing"
)

func TestPinnedDictionaryAndOracle(t *testing.T) {
	r, err := gzip.NewReader(bytes.NewReader(compressedDictionary))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	dictionary, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	const want = "7197c3211ddd98962b036cdf40324d1ea2bfaa12bd028e68faa70111a88e12a8"
	if got := fmt.Sprintf("%x", sha256.Sum256(dictionary)); got != want {
		t.Fatalf("dictionary changed: %s", got)
	}
	raw, err := os.ReadFile("testdata/jieba-next-1.0.0rc1.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden struct {
		DictionarySHA string `json:"dictionary_sha256"`
		Cases         []struct {
			Text   string   `json:"text"`
			Tokens []string `json:"tokens"`
		} `json:"cases"`
	}
	if err = json.Unmarshal(raw, &golden); err != nil {
		t.Fatal(err)
	}
	if golden.DictionarySHA != want || len(golden.Cases) != 328 {
		t.Fatal("unexpected oracle fixture")
	}
	for _, tc := range golden.Cases {
		t.Run(tc.Text, func(t *testing.T) {
			if got := Cut(tc.Text); !slices.Equal(got, tc.Tokens) {
				t.Fatalf("got %q; oracle %q", got, tc.Tokens)
			}
		})
	}
}

func TestRouteTieCandidateLimitAndASCIIGrouping(t *testing.T) {
	// Equal paths choose the longer first word, not the first encountered end.
	d := &dictionary{frequencies: map[string]int{"甲": 1, "乙": 1, "甲乙": 1}, maxRunes: 2}
	if got := d.cutBlock([]rune("甲乙"), nil); !slices.Equal(got, []string{"甲乙"}) {
		t.Fatal(got)
	}
	// Match the pinned Rust implementation's twelve-terminal-candidate cap.
	d = &dictionary{frequencies: map[string]int{}, maxRunes: 13, logTotal: math.Log(1e9)}
	word := ""
	for i := 1; i <= 13; i++ {
		word += "甲"
		d.frequencies[word] = 1
	}
	d.frequencies[word] = 1000000000
	if got := d.cutBlock([]rune(word), nil); !slices.Equal(got, []string{word[:12*3], "甲"}) {
		t.Fatal(got)
	}
}

func TestCutConcurrent(t *testing.T) {
	for i := 0; i < 12; i++ {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			t.Parallel()
			if got := Cut("流浪地球ABC123"); !slices.Equal(got, []string{"流浪", "地球", "ABC123"}) {
				t.Fatal(got)
			}
		})
	}
}

// Run with -run '^$' -bench '^BenchmarkLoadDictionary$' -benchtime=1x.
func BenchmarkLoadDictionary(b *testing.B) {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	var loaded *dictionary
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		loaded = loadDictionary()
	}
	b.StopTimer()
	runtime.GC()
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(loaded)
	if after.HeapAlloc >= before.HeapAlloc {
		b.ReportMetric(float64(after.HeapAlloc-before.HeapAlloc), "retained-B")
	}
}

func BenchmarkCutHistoryQuery(b *testing.B) {
	Cut("warmup")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Cut("流浪地球2.2023.1080p.WEB-DL")
	}
}
