package apprise

import (
	_ "embed"
	"strings"
	"time"
)

// timezoneNames is the original zoneinfo key set, also present in Go 1.26.10.
//
//go:embed timezone-names.txt
var timezoneNames string

var sourceTimezoneNames = func() map[string]bool {
	names := map[string]bool{}
	for zone := range strings.SplitSeq(strings.TrimSuffix(timezoneNames, "\n"), "\n") {
		name := strings.ToLower(zone)
		names[name] = true
		parts := strings.SplitN(name, "/", 4)
		if len(parts) > 1 {
			names[strings.Join(parts[1:min(len(parts), 3)], "/")] = true
		}
	}
	return names
}()

// Providers use this check to validate their URL options. They do not use the
// resulting zone for notification timestamps.
func validSourceTimezone(value string) bool {
	// Python retains the combining dot when lowercasing U+0130.
	name := strings.ToLower(strings.ReplaceAll(strings.TrimFunc(value, sourceSpace), "İ", "i\u0307"))
	switch name {
	case "":
		return false
	case "utc", "z", "gmt", "etc/utc", "etc/gmt", "gmt0", "utc0":
		return true
	}
	if sourceTimezoneNames[name] {
		return true
	}
	if value == "Local" {
		// Go treats Local as a reserved name independently of the zone database.
		return false
	}
	_, err := time.LoadLocation(value)
	return err == nil
}
