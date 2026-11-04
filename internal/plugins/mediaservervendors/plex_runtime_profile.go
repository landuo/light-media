package mediaservervendors

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"light-media/internal/releasemeta/legacyanime"
)

// These are real target runtime facts, not historical or fixture identifiers.
// The App producer owns their acquisition and never publishes their raw values.
type PlexRuntimeHostFacts struct {
	System, Release, NodeName string
	Node                      uint64
}
type PlexRuntimeProfile struct {
	Profile  PlexSDKProfile
	Location *time.Location
	// Source timezone=None yields a naive local datetime. Location supplies its
	// wall-clock conversion; this flag preserves the distinction for a later
	// date serialization boundary. Current S08 refresh does not serialize dates.
	SourceNaiveDates bool
	InvalidTimezone  bool
}

// ResolvePlexRuntimeProfile implements only the SDK settings actually consumed
// by this adapter. Config is PlexConfig._asDict, not an arbitrary global config.
// No files, environment, OS identity or network are accessed by this pure step.
func ResolvePlexRuntimeProfile(config map[string]map[string]string, lookup func(string) (string, bool), host PlexRuntimeHostFacts, now time.Time) (PlexRuntimeProfile, error) {
	var out PlexRuntimeProfile
	if lookup == nil || host.Node >= (uint64(1)<<48) {
		return out, errors.New("Plex runtime facts are unresolved")
	}
	get := func(key, fallback string) string {
		if value, ok := lookup("PLEXAPI_" + strings.ToUpper(strings.ReplaceAll(key, ".", "_"))); ok {
			return value
		}
		parts := strings.Split(key, ".")
		if values, ok := config[parts[0]]; ok {
			if value, present := values[parts[1]]; present {
				return value
			}
		}
		return fallback
	}
	timeout, ok := plexSDKParseInteger(get("plexapi.timeout", "30"))
	if !ok || !timeout.IsInt64() || timeout.Sign() <= 0 || timeout.Int64() > int64((1<<63-1)/int64(time.Second)) {
		// Original invalid cast may be NaN or an arbitrary integer. Do not silently
		// fall back to 30 and pretend the existing int64 HTTP profile represents it.
		return out, &PlexArgumentBoundary{Field: "unrepresentable SDK timeout configuration"}
	}
	zone := legacyanime.Strip(get("plexapi.timezone", "false"))
	// Explicit empty ZoneInfo key raises ValueError before any header lookups.
	if zone == "" {
		return out, plexSDKSourceError("ValueError", "ZoneInfo keys must be normalized relative paths, got: ")
	}
	provides := get("header.provides", "controller")
	platform := get("header.platform", host.System)
	platformVersion := get("header.platform_version", host.Release)
	product := get("header.product", "PlexAPI")
	version := get("header.version", "4.18.1")
	device := get("header.device", platform)
	name := get("header.device_name", host.NodeName)
	identifier := get("header.identifier", fmt.Sprintf("0x%x", host.Node))
	language := get("header.language", "en")
	out.Profile = PlexSDKProfile{
		TimeoutSeconds: timeout.Int64(), ShowSecrets: strings.ToLower(get("log.show_secrets", "")) == "true",
		BaseHeaders: []PlexHeader{{"X-Plex-Platform", platform}, {"X-Plex-Platform-Version", platformVersion}, {"X-Plex-Provides", provides}, {"X-Plex-Product", product}, {"X-Plex-Version", version}, {"X-Plex-Device", device}, {"X-Plex-Device-Name", name}, {"X-Plex-Client-Identifier", identifier}, {"X-Plex-Language", language}, {"X-Plex-Sync-Version", "2"}, {"X-Plex-Features", "external-media"}},
		// Fixed selected Requests2.34.2 dependency profile: no Brotli/Zstd optional
		// module. Actual HTTP decompression must match these advertised algorithms.
		SessionHeaders: []PlexHeader{{"User-Agent", "python-requests/2.34.2"}, {"Accept-Encoding", "gzip, deflate"}, {"Accept", "*/*"}, {"Connection", "keep-alive"}},
	}
	switch strings.ToLower(zone) {
	case "false", "0":
		out.Location = time.Local
		out.SourceNaiveDates = true
	case "true", "1", "local":
		name, offset := now.In(time.Local).Zone()
		out.Location = time.FixedZone(name, offset)
	default:
		location, err := time.LoadLocation(zone)
		if err != nil {
			out.Location = time.Local
			out.SourceNaiveDates = true
			out.InvalidTimezone = true
		} else {
			out.Location = location
		}
	}
	return out, nil
}
