package download

import (
	"encoding/json"
	"math"
	"math/big"
	"strconv"
	"strings"

	"light-media/internal/releasemeta/legacyanime"
)

// The installed wrapper re-filters the daemon's completed result. The server
// filter alone is insufficient: paused/incomplete and error objects also appear
// in some daemon versions. This exact consumed predicate is independent of the
// application's download-management projection.
func iyuuCompletedQB(rows []map[string]any) (json.RawMessage, error) {
	out := []map[string]any{}
	for _, row := range rows {
		state := ""
		if v, ok := row["state"].(string); ok {
			state = strings.ToLower(legacyanime.Strip(v))
		} else if iyuuTruth(row["state"]) {
			return nil, failure("iyuu_torrents", "unsupported_state_type")
		}
		progress, e := iyuuCompareNumber(row["progress"], 1)
		if e != nil {
			return nil, e
		}
		amount, e := iyuuCompareNumber(row["amount_left"], 0)
		if e != nil {
			return nil, e
		}
		excluded := false
		switch state {
		case "allocating", "checkingdl", "checkingup", "downloading", "error", "forceddl", "missingfiles", "metadl", "moving", "queueddl", "stalleddl", "unknown":
			excluded = true
		}
		if progress >= 0 && amount <= 0 && !excluded {
			out = append(out, row)
		}
	}
	return json.Marshal(out)
}
func iyuuCompareNumber(v any, target int64) (int, error) {
	// Source uses `value or 0`; JSON null, false, empty text/containers are zero.
	if !iyuuTruth(v) {
		return new(big.Rat).SetInt64(0).Cmp(new(big.Rat).SetInt64(target)), nil
	}
	var n *big.Rat
	switch x := v.(type) {
	case bool:
		n = new(big.Rat).SetInt64(1)
	case json.Number:
		var ok bool
		n, ok = new(big.Rat).SetString(string(x))
		if !ok {
			return 0, failure("iyuu_torrents", "unsupported_numeric_value")
		}
	default:
		return 0, failure("iyuu_torrents", "unsupported_numeric_type")
	}
	return n.Cmp(new(big.Rat).SetInt64(target)), nil
}

// transmission-rpc exposes these Python properties on Torrent rather than raw
// daemon JSON. Only properties consumed by this plugin are added. Native JSON
// remains in the object for diagnostics and future original wrapper behavior.
func iyuuTRProperties(row map[string]any) error {
	for property, key := range map[string]string{"download_dir": "downloadDir", "total_size": "totalSize", "percent_done": "percentDone"} {
		if v, ok := row[key]; ok {
			if previous, present := row[property]; present && !jsonEqualNative(previous, v) {
				return failure("iyuu_torrents", "conflicting_property")
			}
			if property == "percent_done" {
				if n, e := iyuuTRPercent(v); e == nil {
					v = n
				}
			}
			row[property] = v
		}
	}
	if raw, ok := row["status"].(json.Number); ok {
		// Status.stopped is status value 0 in transmission-rpc 7.0.11.
		row["status_stopped"] = raw == "0"
	}
	return nil
}
func jsonEqualNative(a, b any) bool {
	aa, e := json.Marshal(a)
	if e != nil {
		return false
	}
	bb, e := json.Marshal(b)
	return e == nil && string(aa) == string(bb)
}

func iyuuAddedIDKeys(raw []byte) ([]string, error) {
	var root map[string]json.RawMessage
	if json.Unmarshal(raw, &root) != nil {
		return nil, failure("iyuu_add", "invalid_response_object")
	}
	d := json.NewDecoder(strings.NewReader(string(root["added_torrent_ids"])))
	first, e := d.Token()
	if e != nil || first != json.Delim('{') {
		return nil, failure("iyuu_add", "invalid_added_ids")
	}
	keys := []string{}
	seen := map[string]bool{}
	for d.More() {
		token, e := d.Token()
		if e != nil {
			return nil, e
		}
		key, ok := token.(string)
		if !ok {
			return nil, errIYUUAddedIDs
		}
		var discard json.RawMessage
		if d.Decode(&discard) != nil {
			return nil, errIYUUAddedIDs
		}
		if !seen[key] {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	return keys, nil
}

var errIYUUAddedIDs = failure("iyuu_add", "invalid_added_id_object")

// percent_done is float(fields["percentDone"]), not the raw daemon value.
func iyuuTRPercent(v any) (json.Number, error) {
	text := ""
	switch x := v.(type) {
	case json.Number:
		text = string(x)
	case string:
		text = legacyanime.Strip(x)
	case bool:
		if x {
			text = "1"
		} else {
			text = "0"
		}
	default:
		return "", failure("iyuu_torrents", "invalid_percent_property")
	}
	f, e := strconv.ParseFloat(text, 64)
	if e != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return "", failure("iyuu_torrents", "invalid_percent_property")
	}
	return json.Number(strconv.FormatFloat(f, 'g', -1, 64)), nil
}
