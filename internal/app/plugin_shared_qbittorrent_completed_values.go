package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"strconv"
	"strings"

	"light-media/internal/plugins"
	"light-media/internal/releasemeta/legacyanime"
	"light-media/internal/sourcejson"
)

// Evaluate the two distinct source phases without losing original row fields.
// The whole tag pass must finish before ANY completion predicate is evaluated:
// a later malformed tag is a caught None, even if an earlier progress is bad.
func sharedQBittorrentCompletedRows(ctx context.Context, raw json.RawMessage, tags []json.RawMessage, guard func(context.Context) error) ([]plugins.QBittorrentTorrent, bool, error) {
	empty := []plugins.QBittorrentTorrent{}
	var rows []json.RawMessage
	if json.Unmarshal(raw, &rows) != nil || rows == nil {
		return empty, true, nil
	}
	selected := make([]map[string]json.RawMessage, 0, len(rows))
	for _, rawRow := range rows {
		if err := guard(ctx); err != nil {
			return nil, false, err
		}
		var row map[string]json.RawMessage
		value := bytes.TrimSpace(rawRow)
		if len(value) > 0 && value[0] == '{' {
			if json.Unmarshal(value, &row) != nil {
				return empty, true, nil
			}
		}
		if row == nil && len(tags) > 0 {
			return empty, true, nil
		}
		if len(tags) > 0 {
			var text string
			value, exists := row["tags"]
			if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, &text) != nil {
				return empty, true, nil // source AttributeError is caught.
			}
			have := map[string]bool{}
			for _, tag := range strings.Split(text, ",") {
				have[legacyanime.Strip(tag)] = true
			}
			matches := true
			for _, tag := range tags {
				value := bytes.TrimSpace(tag)
				if len(value) > 0 && (value[0] == '[' || value[0] == '{') {
					return empty, true, nil // set(tags): unhashable list/dict.
				}
				var wanted string
				if len(value) == 0 || value[0] != '"' || json.Unmarshal(value, &wanted) != nil || !have[wanted] {
					matches = false
				}
			}
			if !matches {
				continue
			}
		}
		selected = append(selected, row)
	}
	out := make([]plugins.QBittorrentTorrent, 0, len(selected))
	for _, row := range selected {
		if err := guard(ctx); err != nil {
			return nil, false, err
		}
		if row == nil {
			return nil, false, bad("QB_COMPLETED_SOURCE_EXCEPTION", "AttributeError")
		}
		state := ""
		if sharedQBittorrentTruthy(row["state"]) {
			var err error
			state, err = sourcejson.String(row["state"])
			if err != nil {
				return nil, false, bad("QB_COMPLETED_SOURCE_EXCEPTION", "ValueError")
			}
		}
		state = legacyanime.LowerCase(legacyanime.Strip(state))
		progress, err := sharedQBittorrentNumberCompare(row["progress"], 1)
		if err != nil {
			return nil, false, err
		}
		if progress < 0 {
			continue
		}
		amount, err := sharedQBittorrentNumberCompare(row["amount_left"], 0)
		if err != nil {
			return nil, false, err
		}
		if amount > 0 {
			continue
		}
		switch state {
		case "allocating", "checkingdl", "checkingup", "downloading", "error", "forceddl", "missingfiles", "metadl", "moving", "queueddl", "stalleddl", "unknown":
			continue
		}
		out = append(out, plugins.QBittorrentTorrent{Fields: row})
	}
	return out, false, nil
}

func sharedQBittorrentTruthy(raw json.RawMessage) bool {
	value := bytes.TrimSpace(raw)
	if len(value) == 0 || string(value) == "null" || string(value) == "false" {
		return false
	}
	switch value[0] {
	case '"':
		var text string
		return json.Unmarshal(value, &text) == nil && text != ""
	case '[':
		var list []json.RawMessage
		return json.Unmarshal(value, &list) == nil && len(list) > 0
	case '{':
		var object map[string]json.RawMessage
		return json.Unmarshal(value, &object) == nil && len(object) > 0
	case 't':
		return true
	default:
		if strings.ContainsAny(string(value), ".eE") {
			n, _ := strconv.ParseFloat(string(value), 64)
			return n != 0
		}
		n, ok := new(big.Int).SetString(string(value), 10)
		return ok && n.Sign() != 0
	}
}

// Python json.loads uses arbitrary-precision ints but binary64 for decimals
// and exponents. Keep raw lexemes in the result; compare source-shaped numbers.
func sharedQBittorrentNumberCompare(raw json.RawMessage, target int64) (int, error) {
	if !sharedQBittorrentTruthy(raw) {
		return new(big.Int).Cmp(new(big.Int).SetInt64(target)), nil
	}
	value := bytes.TrimSpace(raw)
	if string(value) == "true" {
		return new(big.Int).SetInt64(1).Cmp(new(big.Int).SetInt64(target)), nil
	}
	if len(value) == 0 || value[0] == '"' || value[0] == '[' || value[0] == '{' {
		return 0, bad("QB_COMPLETED_SOURCE_EXCEPTION", "TypeError")
	}
	if strings.ContainsAny(string(value), ".eE") {
		n, err := strconv.ParseFloat(string(value), 64)
		if err != nil && !errors.Is(err, strconv.ErrRange) {
			return 0, bad("QB_COMPLETED_SOURCE_EXCEPTION", "ValueError")
		}
		if math.IsNaN(n) {
			return 0, bad("QB_COMPLETED_SOURCE_EXCEPTION", "ValueError")
		}
		if n < float64(target) {
			return -1, nil
		}
		if n > float64(target) {
			return 1, nil
		}
		return 0, nil
	}
	n, ok := new(big.Int).SetString(string(value), 10)
	if !ok {
		return 0, bad("QB_COMPLETED_SOURCE_EXCEPTION", "ValueError")
	}
	return n.Cmp(new(big.Int).SetInt64(target)), nil
}
