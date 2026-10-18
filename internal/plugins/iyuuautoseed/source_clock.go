package iyuuautoseed

import (
	"context"
	"errors"
	"math"
	"time"
)

// The optional owner API clock is consumed exactly where the source helper
// calls int(time.time()), including its one site-hash retry. Pure helper users
// retain their supplied source clock; the actual App persists each reading.
type sourceAPIClock interface {
	SourceUnix(context.Context) (int64, error)
}

func sourceUnixTime(t time.Time) (int64, error) {
	// CPython time.time() is a double and int() truncates toward zero. Preserve
	// that observable behavior, including negative fractions and second rounding.
	value := math.Trunc(float64(t.Unix()) + float64(t.Nanosecond())/1e9)
	if math.IsNaN(value) || math.IsInf(value, 0) || value < -9223372036854775808.0 || value >= 9223372036854775808.0 {
		return 0, errors.New("source clock outside timestamp range")
	}
	return int64(value), nil
}

func (a DurableAPI) SourceUnix(ctx context.Context) (int64, error) {
	var seconds int64
	err := a.run.step(ctx, "source-time", nil, false, &seconds, func() (any, string, error) {
		if a.clock == nil {
			return nil, "read", errors.New("source clock not configured")
		}
		value, e := sourceUnixTime(a.clock())
		return value, "read", e
	})
	return seconds, err
}
