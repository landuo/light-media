package main

import (
	"slices"
	"strings"
	"testing"
)

func TestExecutableAdviceOnlyVisitsReadOnlyMappingsOfRunningFile(t *testing.T) {
	const maps = `1000-2000 r-xp 00000000 fe:00 52 /bin/light media
2000-4000 r--p 00001000 fe:00 52 /bin/light media (deleted)
4000-5000 rw-p 00003000 fe:00 52 /bin/light media
5000-6000 r--p 00000000 00:00 0
6000-7000 r-xp 00000000 fe:01 52 /different
7000-8000 r-xp 00000000 fe:00 53 /different
ffff-eeee r-xp 00000000 fe:00 52 /invalid`
	var got [][2]uintptr
	visitExecutableMappings(strings.NewReader(maps), "fe:00", "52", "", func(start, length uintptr) {
		got = append(got, [2]uintptr{start, length})
	})
	if !slices.Equal(got, [][2]uintptr{{0x1000, 0x1000}, {0x2000, 0x2000}}) {
		t.Fatalf("unexpected executable ranges: %v", got)
	}
}

func TestExecutableAdviceBtrfsSubvolumeDevice(t *testing.T) {
	const maps = `1000-2000 r-xp 00000000 00:19 23          /config/a  b
2000-3000 r--p 00001000 00:19 23          /config/a  b
3000-4000 r--p 00000000 00:19 23          /other/a  b
4000-5000 r--p 00000000 00:19 24          /config/a  b
5000-6000 rw-p 00001000 00:19 23          /config/a  b`
	var got []uintptr
	visitExecutableMappings(strings.NewReader(maps), "00:1a", "23", "/config/a  b", func(start, length uintptr) { got = append(got, start) })
	if !slices.Equal(got, []uintptr{0x1000, 0x2000}) {
		t.Fatal(got)
	}
}
