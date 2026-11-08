package schedule

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The declaration carries only the system-local choice. Paths come exclusively
// from the host's TZ environment or these two source-defined system locations;
// workers cannot supply a timezone file, its contents or another lookup root.
func systemLocalLocation() (*time.Location, error) {
	value, present := os.LookupEnv("TZ")
	if !present || value == "" {
		// Go treats TZ="" as UTC. tzlocal treats it as unset. This flag represents
		// the source's anonymous local-file branch, after worker name discovery.
		return anonymousLocalLocation(os.Stat, os.ReadFile)
	}
	value = strings.TrimPrefix(value, ":")
	var err error
	if filepath.IsAbs(value) {
		var raw []byte
		raw, err = os.ReadFile(value)
		if err == nil {
			_, err = time.LoadLocationFromTZData("local", raw)
		}
	} else {
		// Validate before time.Local, whose initializer silently falls back to UTC
		// when TZ names a missing zone or an unreadable/malformed timezone file.
		if value == "" || value == "Local" {
			return nil, errTrigger
		}
		_, err = time.LoadLocation(value)
	}
	if err != nil {
		return nil, errTrigger
	}
	return time.Local, nil
}

func anonymousLocalLocation(stat func(string) (os.FileInfo, error), readFile func(string) ([]byte, error)) (*time.Location, error) {
	for _, path := range []string{"/etc/localtime", "/usr/local/etc/localtime"} {
		// Python genericpath.exists catches every stat OSError, including
		// EACCES/EIO. A successful stat followed by an open/read error raises.
		if _, err := stat(path); err != nil {
			continue
		}
		raw, err := readFile(path)
		if err != nil {
			return nil, errTrigger
		}
		location, err := time.LoadLocationFromTZData("local", raw)
		if err != nil {
			return nil, errTrigger
		}
		return location, nil
	}
	// tzlocal warns and uses UTC only when neither canonical path passes exists.
	return time.UTC, nil
}
