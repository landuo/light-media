package autosignin

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// Private BackgroundScheduler uses the stripped Settings.TZ field, whose
// default applies only when the environment field is absent.
func privateTimezone() (string, error) {
	name, exists := os.LookupEnv("TZ")
	if !exists {
		name = "Asia/Shanghai"
	} else {
		name = strings.TrimFunc(name, sourcePythonSpace)
	}
	if !sourceZoneNameValid(name) {
		return "", errors.New("invalid private scheduler timezone")
	}
	return name, nil
}
func sourceZoneNameValid(name string) bool {
	if name == "" || name == "Local" {
		return false
	}
	_, e := time.LoadLocation(name)
	return e == nil
}

type localZoneConfig struct{ file, name string }

// sourcePublicTimezone follows the installed tzlocal Linux name-discovery
// branches. Anonymous TZif data uses the explicit host local_timezone marker,
// so no timezone bytes or filesystem paths cross the plugin boundary.
// root is '/' in production and mirrors tzlocal's _root isolated-test seam.
func sourcePublicTimezone(root string) (string, error) {
	name, _, e := resolveSourcePublicTimezone(root)
	return name, e
}
func resolveSourcePublicTimezone(root string) (name string, warnings []string, err error) {
	zone, e := resolveSourcePublicZone(root, func(message string) { warnings = append(warnings, message) })
	return zone, warnings, e
}
func resolveSourcePublicZone(root string, warn func(string)) (string, error) {
	env := os.Getenv("TZ")
	if env != "" {
		env = strings.TrimPrefix(env, ":")
		if filepath.IsAbs(env) {
			if _, e := os.Stat(env); e == nil {
				return namedLocalZoneFile(root, env)
			}
		}
		if !sourceZoneNameValid(env) {
			return "", errors.New("invalid process-local timezone")
		}
		return env, nil
	}
	configs := []localZoneConfig{}
	add := func(file, name string) {
		for i := range configs {
			if configs[i].file == file {
				configs[i].name = name
				return
			}
		}
		configs = append(configs, localZoneConfig{file, name})
	}
	for _, file := range []string{"etc/timezone", "var/db/zoneinfo"} {
		raw, e := os.ReadFile(filepath.Join(root, file))
		if e != nil {
			continue
		}
		ascii := true
		for _, b := range raw {
			if b >= 128 {
				ascii = false
				break
			}
		}
		if !ascii {
			continue
		}
		value := strings.Trim(string(raw), "/ \t\r\n")
		for _, line := range strings.FieldsFunc(value, func(r rune) bool { return r == '\n' || r == '\r' || r == '\v' || r == '\f' || r >= 0x1c && r <= 0x1e }) {
			line, _, _ = strings.Cut(line, " ")
			line, _, _ = strings.Cut(line, "#")
			if line != "" {
				add(file, strings.ReplaceAll(line, " ", "_"))
			}
		}
	}
	for _, file := range []string{"etc/sysconfig/clock", "etc/conf.d/clock"} {
		raw, e := os.ReadFile(filepath.Join(root, file))
		if e != nil || !utf8.Valid(raw) {
			continue
		}
		data := strings.ReplaceAll(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\r", "\n")
		for _, line := range strings.SplitAfter(data, "\n") {
			rest := strings.TrimLeftFunc(line, sourcePythonSpace)
			if strings.HasPrefix(rest, "TIMEZONE") {
				rest = strings.TrimPrefix(rest, "TIMEZONE")
			} else if strings.HasPrefix(rest, "ZONE") {
				rest = strings.TrimPrefix(rest, "ZONE")
			} else {
				continue
			}
			rest = strings.TrimLeftFunc(rest, sourcePythonSpace)
			if !strings.HasPrefix(rest, "=") {
				continue
			}
			rest = strings.TrimLeftFunc(rest[1:], sourcePythonSpace)
			if !strings.HasPrefix(rest, "\"") {
				continue
			}
			rest = rest[1:]
			end := strings.IndexByte(rest, '"')
			if end < 0 {
				warn(fmt.Sprintf("Syntax error in %s. Ignoring line: %s", filepath.Join(root, file), line))
				continue
			}
			add(file, strings.ReplaceAll(rest[:end], " ", "_"))
		}
	}
	local := filepath.Join(root, "etc/localtime")
	if info, e := os.Lstat(local); e == nil && info.Mode()&os.ModeSymlink != 0 {
		if target, e := filepath.EvalSymlinks(local); e == nil {
			for _, suffix := range sourceZoneSuffixes(target) {
				if sourceZoneNameValid(suffix) {
					add("etc/localtime", suffix)
					break
				}
			}
		}
	}
	unique := func(values []localZoneConfig) int {
		seen := map[string]bool{}
		for _, v := range values {
			path := filepath.Join(root, "usr/share/zoneinfo", v.name)
			if real, e := filepath.EvalSymlinks(path); e == nil {
				path = real
			}
			seen[path] = true
		}
		return len(seen)
	}
	if len(configs) > 1 && unique(configs) != 1 {
		hasDeprecated := false
		for _, v := range configs {
			hasDeprecated = hasDeprecated || v.file == "etc/timezone"
		}
		if hasDeprecated {
			warn("/etc/timezone is deprecated in some distros, and no longer reliable. tzlocal is ignoring it, and you can likely delete it.")
		}
		filtered := []localZoneConfig{}
		for _, v := range configs {
			if v.file != "etc/timezone" {
				filtered = append(filtered, v)
			}
		}
		configs = filtered
		if unique(configs) != 1 {
			return "", errors.New("conflicting process-local timezone configuration")
		}
	}
	if len(configs) > 0 {
		if !sourceZoneNameValid(configs[0].name) {
			return "", errors.New("invalid process-local timezone configuration")
		}
		return configs[0].name, nil
	}
	for _, file := range []string{"etc/localtime", "usr/local/etc/localtime"} {
		path := filepath.Join(root, file)
		if _, e := os.Stat(path); e == nil {
			return namedLocalZoneFile(root, path)
		}
	}
	warn("Can not find any timezone configuration, defaulting to UTC.")
	return "UTC", nil
}
func sourceZoneSuffixes(path string) []string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	out := []string{}
	for i := 1; i < len(parts); i++ {
		out = append(out, strings.Join(parts[i:], "/"))
	}
	return out
}
func namedLocalZoneFile(_ string, path string) (string, error) {
	raw, e := os.ReadFile(path)
	if e != nil {
		return "", e
	}
	if _, e = time.LoadLocationFromTZData("local", raw); e != nil {
		return "", e
	}
	return "local", nil
}
