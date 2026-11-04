package app

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/unix"
	"light-media/internal/plugins/mediaservervendors"
)

// A real process-owned Linux provider. It reuses the existing accepted native
// MAC selection contract; it does not call Python/libuuid/ip/ifconfig or use the
// center UID's unrelated filesystem hash as a Plex client identifier.
var originalPlexHostIdentity struct {
	once  sync.Once
	facts mediaservervendors.PlexRuntimeHostFacts
	err   error
}

func originalPlexLinuxHostFacts() (mediaservervendors.PlexRuntimeHostFacts, error) {
	originalPlexHostIdentity.once.Do(func() {
		var u unix.Utsname
		if err := unix.Uname(&u); err != nil {
			originalPlexHostIdentity.err = err
			return
		}
		text := func(value []byte) string {
			for i, b := range value {
				if b == 0 {
					return string(value[:i])
				}
			}
			return string(value)
		}
		node, err := originalPluginMACNode()
		if err != nil {
			// uuid.getnode's final fallback has a random 48-bit node with multicast
			// bit set. It is cached for the process, not emitted in diagnostics.
			var raw [6]byte
			if _, err = io.ReadFull(rand.Reader, raw[:]); err != nil {
				originalPlexHostIdentity.err = err
				return
			}
			raw[0] |= 1
			node = 0
			for _, b := range raw {
				node = node<<8 | uint64(b)
			}
		}
		originalPlexHostIdentity.facts = mediaservervendors.PlexRuntimeHostFacts{
			System: text(u.Sysname[:]), Release: text(u.Release[:]), NodeName: text(u.Nodename[:]), Node: node,
		}
	})
	return originalPlexHostIdentity.facts, originalPlexHostIdentity.err
}

// LoadOriginalPlexRuntimeProfile resolves the independent Go deployment's
// current configuration. Default storage is its own DataDir, never a required
// MoviePilot user home. PLEXAPI_CONFIG_PATH is an explicit operator override.
// This narrow target-platform path adaptation does not change field precedence.
func LoadOriginalPlexRuntimeProfile(ctx context.Context, dataDir string) (mediaservervendors.PlexSDKProfile, *time.Location, error) {
	var zero mediaservervendors.PlexSDKProfile
	if err := ctx.Err(); err != nil {
		return zero, nil, err
	}
	if dataDir == "" || !filepath.IsAbs(dataDir) {
		return zero, nil, errors.New("Plex runtime profile requires owned absolute data directory")
	}
	path := filepath.Join(dataDir, "plexapi", "config.ini")
	if explicit, ok := os.LookupEnv("PLEXAPI_CONFIG_PATH"); ok {
		path = explicit
	}
	raw, readErr := os.ReadFile(path)
	if err := ctx.Err(); err != nil {
		return zero, nil, err
	}
	if readErr != nil {
		// ConfigParser.read ignores OSError (including missing/unreadable file).
		// Parse/UTF-8 errors below are NOT replaced with an empty configuration.
		raw = nil
	}
	config, err := mediaservervendors.ParsePlexRuntimeINI(raw)
	if err != nil {
		return zero, nil, err
	}
	facts, err := originalPlexLinuxHostFacts()
	if err != nil {
		return zero, nil, fmt.Errorf("Plex Linux runtime identity unavailable: %w", err)
	}
	result, err := mediaservervendors.ResolvePlexRuntimeProfile(config, os.LookupEnv, facts, time.Now())
	if current := ctx.Err(); current != nil {
		return zero, nil, current
	}
	if err != nil {
		return zero, nil, err
	}
	return result.Profile, result.Location, nil
}
