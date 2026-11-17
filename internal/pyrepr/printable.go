// Package pyrepr implements the two CPython value formatters the source-shaped
// JSON writer needs: str repr and float str. It is a leaf package so callers
// that only format values do not link the template engine.
package pyrepr

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/json"
	"sort"
	"sync"
)

// The table is generated offline from the exact CPython 3.12 Unicode database;
// Go or OS Unicode upgrades must not change the formatting result.
//
//go:embed unicode-cpython312.json.gz
var unicodeCompressed []byte

var printableOnce sync.Once
var printableRanges [][2]rune

// The printable set is only needed by the first escaped repr. Loading it lazily
// keeps processes that never format a value from paying for the table at start.
func loadPrintable() {
	z, err := gzip.NewReader(bytes.NewReader(unicodeCompressed))
	if err != nil {
		panic(err)
	}
	defer z.Close()
	var data struct {
		Version   string
		Printable [][2]rune
	}
	if err = json.NewDecoder(z).Decode(&data); err != nil {
		panic(err)
	}
	if data.Version != "15.0.0" {
		panic("unexpected CPython Unicode version")
	}
	printableRanges = data.Printable
}

// Printable reports whether CPython's str repr writes the rune literally.
func Printable(r rune) bool {
	printableOnce.Do(loadPrintable)
	at := sort.Search(len(printableRanges), func(i int) bool { return printableRanges[i][1] >= r })
	return at < len(printableRanges) && printableRanges[at][0] <= r
}
