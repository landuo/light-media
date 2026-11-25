//go:build ignore

// Generate the immutable lookup table from the pinned jieba-next dictionary.
package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"log"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

func main() {
	if err := generate(); err != nil {
		log.Fatal(err)
	}
}

func generate() error {
	f, err := os.Open("dict.txt.gz")
	if err != nil {
		return err
	}
	defer f.Close()
	r, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer r.Close()
	weights := make(map[string]uint32)
	var total uint64
	var longest int
	s := bufio.NewScanner(r)
	for s.Scan() {
		fields := strings.Fields(s.Text())
		if len(fields) < 2 {
			return fmt.Errorf("invalid dictionary entry")
		}
		frequency, err := strconv.ParseUint(fields[1], 10, 32)
		if err != nil {
			return err
		}
		weights[fields[0]] = uint32(frequency)
		total += frequency // The source sums duplicate lines and looks up the last value.
		longest = max(longest, utf8.RuneCountInString(fields[0]))
	}
	if err := s.Err(); err != nil {
		return err
	}
	words := slices.Sorted(maps.Keys(weights))
	var index, data bytes.Buffer
	write := func(v uint32) { index.Write(binary.LittleEndian.AppendUint32(nil, v)) }
	write(uint32(len(words)))
	write(uint32(longest))
	index.Write(binary.LittleEndian.AppendUint64(nil, total))
	for _, word := range words {
		write(uint32(data.Len()))
		write(weights[word])
		data.WriteString(word)
	}
	write(uint32(data.Len())) // Sentinel word offset.
	index.Write(data.Bytes())
	return os.WriteFile("dict.idx", index.Bytes(), 0644)
}
