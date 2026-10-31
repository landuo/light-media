// Package siteaccess implements the fixed source SiteOper access-result state
// transitions. It owns no database, clock, site lookup, network or authority.
package siteaccess

import (
	"errors"
	"math"
	"math/big"
	"sort"
)

var (
	// ErrIntegerRange is an explicit boundary of the Go/SQLite int64 domain,
	// not a wrapped counter or a successful source None result.
	ErrIntegerRange   = errors.New("site access statistic exceeds int64 storage range")
	ErrDomainMismatch = errors.New("site access statistic does not match the exact requested domain")
)

// Record represents the eight source SiteStatistic fields in the finite
// ordinary-integer/nil domain. Nil ID means a new row: only SQLite assigns its
// real identity. Nil Seconds and nil Note are different from zero and {}.
type Record struct {
	ID           *int64           `json:"id"`
	Domain       string           `json:"domain"`
	Success      int64            `json:"success"`
	Fail         int64            `json:"fail"`
	Seconds      *int64           `json:"seconds"`
	LastState    int64            `json:"lst_state"`
	LastModified string           `json:"lst_mod_date"`
	Note         map[string]int64 `json:"note"`
}

func cloneInteger(value *int64) *int64 {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

// Clone keeps all fields and nullable distinctions without sharing writable
// pointers/maps with a prior row or a caller's next transaction.
func (r Record) Clone() Record {
	r.ID = cloneInteger(r.ID)
	r.Seconds = cloneInteger(r.Seconds)
	if r.Note != nil {
		note := make(map[string]int64, len(r.Note))
		for key, value := range r.Note {
			note[key] = value
		}
		r.Note = note
	}
	return r
}

// Success preserves MoviePilot 8a83efe's algorithm, including its pre-truncation
// denominator and zero-average fallback. timestamp is the caller's already
// captured source-format wall-clock value; same-second keys simply overwrite.
func Success(prior *Record, domain string, seconds *int64, timestamp string) (Record, error) {
	if prior == nil {
		elapsed := int64(1)
		if seconds != nil && *seconds != 0 {
			elapsed = *seconds
		}
		note := map[string]int64{}
		if seconds != nil {
			note[timestamp] = elapsed
		}
		return Record{Domain: domain, Success: 1, Fail: 0, Seconds: &elapsed, LastState: 0, LastModified: timestamp, Note: note}, nil
	}
	if prior.Domain != domain {
		return Record{}, ErrDomainMismatch
	}
	if prior.Success == math.MaxInt64 {
		return Record{}, ErrIntegerRange
	}
	out := prior.Clone()
	if out.Note == nil {
		out.Note = map[string]int64{}
	}
	if seconds != nil {
		elapsed := *seconds
		if elapsed == 0 {
			elapsed = 1
		}
		out.Note[timestamp] = elapsed
		count := int64(len(out.Note)) // Count BEFORE retaining the ten newest keys.
		if count > 10 {
			keys := make([]string, 0, len(out.Note))
			for key := range out.Note {
				keys = append(keys, key)
			}
			sort.Sort(sort.Reverse(sort.StringSlice(keys)))
			retained := make(map[string]int64, 10)
			for _, key := range keys[:10] {
				retained[key] = out.Note[key]
			}
			out.Note = retained
		}
		// Python sums with arbitrary-precision integers. Avoid an intermediate
		// int64 overflow even when the final average still fits the storage type.
		sum := new(big.Int)
		for _, value := range out.Note {
			sum.Add(sum, big.NewInt(value))
		}
		average, remainder := new(big.Int), new(big.Int)
		average.QuoRem(sum, big.NewInt(count), remainder)
		if sum.Sign() < 0 && remainder.Sign() != 0 {
			average.Sub(average, big.NewInt(1))
		}
		if !average.IsInt64() {
			return Record{}, ErrIntegerRange
		}
		if value := average.Int64(); value != 0 {
			out.Seconds = &value
		}
	}
	out.Success++
	out.LastState = 0
	out.LastModified = timestamp
	return out, nil
}

// Fail leaves seconds/note and the success count untouched. New fail records
// deliberately have null Seconds and Note, not invented zero/empty defaults.
func Fail(prior *Record, domain, timestamp string) (Record, error) {
	if prior == nil {
		return Record{Domain: domain, Success: 0, Fail: 1, LastState: 1, LastModified: timestamp}, nil
	}
	if prior.Domain != domain {
		return Record{}, ErrDomainMismatch
	}
	if prior.Fail == math.MaxInt64 {
		return Record{}, ErrIntegerRange
	}
	out := prior.Clone()
	out.Fail++
	out.LastState = 1
	out.LastModified = timestamp
	return out, nil
}
