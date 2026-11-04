package app

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"light-media/internal/plugins/mediaservervendors"
)

// The resolved default-header profile is a required source-compatibility input;
// this function does not turn a test's Content-Type-only profile into production
// defaults. Clock and random values are genuine target-runtime observations.
// Api.get_authx retains the source's nonce-before-clock evaluation order and
// performs float-seconds * 1000 followed by truncation itself.
func originalNativeTrimeProtocol(accessHeaders []mediaservervendors.TrimeHTTPHeader) (mediaservervendors.TrimeAPIProtocol, error) {
	if accessHeaders == nil {
		return mediaservervendors.TrimeAPIProtocol{}, errors.New("native Trime source default header profile is unresolved")
	}
	return mediaservervendors.TrimeAPIProtocol{
		UserAgent:     originalPluginServerUserAgent(),
		AccessHeaders: append([]mediaservervendors.TrimeHTTPHeader{}, accessHeaders...),
		Nonce:         originalNativeTrimeNonce,
		Seconds:       originalNativeTrimeSeconds,
		// The actual App builder installs its typed redacted diagnostic sink before
		// any API construction. Do not supply a silent default logger here.
	}, nil
}

func originalNativeTrimeNonce(ctx context.Context) (int, error) {
	if ctx == nil {
		return 0, errors.New("native Trime nonce requires invocation context")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	// randint(100000,999999): uniform inclusive six-digit domain. No fixed seed,
	// sample value, modulo-biased conversion, or simulated Python RNG state.
	nonce := 100000 + rand.IntN(900000)
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return nonce, nil
}
func originalNativeTrimeSeconds(ctx context.Context) (float64, error) {
	if ctx == nil {
		return 0, errors.New("native Trime clock requires invocation context")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	seconds := originalNativeTrimeClockSeconds(time.Now())
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return seconds, nil
}
func originalNativeTrimeClockSeconds(now time.Time) float64 {
	// Keep the wall-clock observation in float seconds. Do not shortcut the
	// original API's later float multiplication/truncation with UnixMilli.
	return float64(now.UnixNano()) / 1e9
}
