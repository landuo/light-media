package app

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// Read only aggregate RSS from this process. No address, path, or raw mapping
// leaves this diagnostic. Unnamed anonymous pages cannot be attributed to a
// particular allocator on kernels without named anonymous mappings.
func residentClasses() (map[string]uint64, error) {
	f, err := os.Open("/proc/self/smaps")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parseResidentClasses(f)
}
func parseResidentClasses(r io.Reader) (map[string]uint64, error) {
	s := bufio.NewScanner(r)
	out := map[string]uint64{}
	class := ""
	n := 0
	for s.Scan() {
		line := s.Text()
		if strings.HasPrefix(line, "Rss:") {
			fields := strings.Fields(line)
			if class == "" || len(fields) != 3 || fields[2] != "kB" {
				return nil, fmt.Errorf("invalid smaps RSS")
			}
			v, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				return nil, err
			}
			out[class] += v * 1024
			n++
			continue
		}
		h, _, _ := strings.Cut(line, " ")
		if !strings.Contains(h, "-") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		switch {
		case strings.Contains(line, "[anon: Go: heap]"):
			class = "go_heap"
		case strings.Contains(line, "[anon: Go:"):
			class = "go_other"
		case len(fields) == 5:
			class = "anonymous_unlabelled"
		case strings.HasPrefix(fields[5], "/"):
			if strings.Contains(fields[1], "x") {
				class = "file_executable"
			} else {
				class = "file_other"
			}
		case strings.HasPrefix(fields[5], "[stack"):
			class = "thread_stack"
		case fields[5] == "[heap]":
			class = "brk_heap"
		default:
			class = "other"
		}
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, fmt.Errorf("no smaps RSS")
	}
	return out, nil
}
