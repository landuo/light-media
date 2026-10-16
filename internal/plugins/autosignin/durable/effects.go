package durable

import (
	"context"
	"encoding/json"
	"errors"
	source "light-media/internal/plugins/autosignin"
	"sort"
	"time"
)

type Cache struct {
	Lane *Lane
	Key  string
}

func (c Cache) Ensure(ctx context.Context) error {
	return c.Lane.Effect(ctx, "cache.ensure", c.Key, nil, Mutation{Kind: "cache.ensure", Key: c.Key})
}
func (c Cache) Read(ctx context.Context) ([]byte, error) {
	raw, err := c.Lane.Value(ctx, "cache.read", c.Key, func() (json.RawMessage, error) {
		s, err := c.Lane.execution.Store.State(ctx, c.Lane.execution.Binding)
		if err != nil {
			return nil, err
		}
		return marshal(struct {
			Value []byte
			Found bool
		}{s.Cache[c.Key], s.Cache[c.Key] != nil}), nil
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		Value []byte
		Found bool
	}
	if json.Unmarshal(raw, &out) != nil {
		return nil, ErrStore
	}
	if !out.Found {
		return nil, source.ErrTjuptCacheIO
	}
	return out.Value, nil
}
func (c Cache) Write(ctx context.Context, data []byte) error {
	err := c.Lane.Effect(ctx, "cache.write", c.Key, data, Mutation{Kind: "cache.write", Key: c.Key, Value: marshal(data)})
	var known *source.SourceFailure
	if errors.As(err, &known) && (known.Class == "OSError" || known.Class == "IOError" || known.Class == "FileNotFoundError") {
		return source.ErrTjuptCacheIO
	}
	return err
}
func (l *Lane) Statistic(ctx context.Context, domain string, success bool, seconds int, now time.Time) error {
	return l.StatisticClock(ctx, domain, success, seconds, func() time.Time { return now })
}
func (l *Lane) StatisticClock(ctx context.Context, domain string, success bool, seconds int, now func() time.Time) error {
	// The timestamp is recorded with the effect arguments, so replay must use a
	// recorded clock, never a fresh wall time after a process restart.
	stamp, err := l.Value(ctx, "stat.clock", nil, func() (json.RawMessage, error) { return marshal(now().Format("2006-01-02 15:04:05")), nil })
	if err != nil {
		return err
	}
	var date string
	json.Unmarshal(stamp, &date)
	request := struct {
		Success bool
		Seconds *int
		Date    string
	}{success, &seconds, date}
	return l.Effect(ctx, "stat", domain, request, Mutation{Kind: "stat.apply", Key: domain, Value: marshal(request)})
}
func UpdateStatistic(stat Statistic, exists, success bool, seconds *int, date string) Statistic {
	if !exists {
		stat = Statistic{}
	}
	stat.LastDate = date
	if !success {
		stat.Fail++
		stat.LastState = 1
		return stat
	}
	stat.Success++
	stat.LastState = 0
	if !exists {
		stat.Note = map[string]int{}
		value := 1
		if seconds != nil && *seconds != 0 {
			value = *seconds
		}
		stat.Seconds = &value
		if seconds != nil {
			stat.Note[date] = value
		}
		return stat
	}
	note := map[string]int{}
	for k, v := range stat.Note {
		note[k] = v
	}
	var average *int
	if seconds != nil {
		value := *seconds
		if value == 0 {
			value = 1
		}
		note[date] = value
		count := len(note)
		if count > 10 {
			keys := []string{}
			for key := range note {
				keys = append(keys, key)
			}
			sort.Sort(sort.Reverse(sort.StringSlice(keys)))
			retained := map[string]int{}
			for _, key := range keys[:10] {
				retained[key] = note[key]
			}
			note = retained
		}
		sum := 0
		for _, v := range note {
			sum += v
		}
		value = sum / count
		if sum < 0 && sum%count != 0 {
			value--
		}
		average = &value
	}
	if average != nil && *average != 0 {
		stat.Seconds = average
	}
	stat.Note = note
	return stat
}
