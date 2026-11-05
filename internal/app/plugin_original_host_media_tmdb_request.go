package app

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"strings"
	"sync"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/releasemeta/legacyanime"
)

type originalHostTMDBRequestState struct {
	mu        sync.Mutex
	remaining *big.Int
	reset     *big.Int
}

func (a *App) originalHostMediaTMDBProviderKey(arguments any) string {
	credentials, _ := json.Marshal(a.Settings().TMDB)
	location, err := time.LoadLocation(identifiersTimezone())
	if err != nil {
		location = time.Local
	}
	day := time.Unix(int64(a.originalHostMediaWallTime()), 0).In(location).Format("20060102")
	key, _ := json.Marshal([]any{arguments, a.originalHostMediaEffectiveLocale(), tokenHash(string(credentials)), day})
	return string(key)
}

func (a *App) originalHostMediaProviderSleep(ctx context.Context, seconds float64) error {
	if sleep := a.originalHostMediaRecognition.Sleep; sleep != nil {
		return sleep(ctx, seconds)
	}
	if seconds < 0 || math.IsInf(seconds, 0) || math.IsNaN(seconds) || seconds > float64(math.MaxInt64)/float64(time.Second) {
		return errors.New("invalid source provider delay")
	}
	timer := time.NewTimer(time.Duration(seconds * float64(time.Second)))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Source TMDb.request snapshots valid JSON before HTTP-business errors and
// before processing its rate headers. Both successful and error envelopes can
// therefore be cached; malformed JSON/transport failure cannot.
func (a *App) originalHostMediaTMDBResponse(ctx context.Context, receiver, key string, load func() (catalog.SourceRecognitionResponse, error)) (json.RawMessage, error) {
	value, _ := a.originalHostMediaProviderCaches.LoadOrStore("tmdb-rate:"+receiver, &originalHostTMDBRequestState{remaining: big.NewInt(40)})
	state := value.(*originalHostTMDBRequestState)
	for {
		value, err := a.originalHostMediaProviderCached(ctx, "themoviedb", "request", key, false, func() (any, error) {
			response, err := load()
			if err != nil {
				return nil, err
			}
			return response, nil
		})
		if err != nil {
			return nil, err
		}
		response, ok := value.(catalog.SourceRecognitionResponse)
		if !ok {
			return nil, errors.New("invalid TMDB provider snapshot")
		}
		state.mu.Lock()
		for header, raw := range response.Headers {
			name := strings.ToLower(header)
			if name != "x-ratelimit-remaining" && name != "x-ratelimit-reset" {
				continue
			}
			if len(raw) == 0 {
				state.mu.Unlock()
				return nil, errors.New("invalid TMDB rate header")
			}
			number, err := legacyanime.Integer(raw[0])
			if err != nil {
				state.mu.Unlock()
				return nil, err
			}
			if name == "x-ratelimit-remaining" {
				state.remaining = number
			} else {
				state.reset = number
			}
		}
		wait := new(big.Int)
		if state.remaining.Sign() < 1 {
			if state.reset == nil {
				state.mu.Unlock()
				return nil, errors.New("missing TMDB rate reset")
			}
			wait.Sub(state.reset, big.NewInt(int64(a.originalHostMediaWallTime())))
			wait.Abs(wait)
		}
		state.mu.Unlock()
		if wait.Sign() > 0 {
			seconds, _ := new(big.Float).SetInt(wait).Float64()
			if err := a.originalHostMediaProviderSleep(ctx, seconds); err != nil {
				return nil, err
			}
			ctx = context.WithValue(ctx, originalHostMediaFreshContextKey{}, true)
			continue
		}
		decoded, err := decodeSourceJSON(response.JSON)
		if err != nil {
			return nil, err
		}
		if fields := sourceMap(decoded); fields != nil {
			if _, present := fields["errors"]; present {
				return nil, errors.New("TMDB provider error")
			}
			if fields["success"] == false {
				return nil, errors.New("TMDB provider error")
			}
		}
		return append(json.RawMessage(nil), response.JSON...), nil
	}
}
