package downloadersource

import (
	"encoding/json"
	"strings"
)

// Filter applies the installed wrapper's tag/status predicate. A nonempty
// CaughtClass is the wrapper's actual exception->None branch, not an empty ACK.
func Filter(kind string, rows []map[string]any, tags string) ([]map[string]any, string, error) {
	out := []map[string]any{}
	required := []string{}
	if tags != "" {
		required = strings.Split(tags, ",")
	}
	for _, r := range rows {
		if kind == "transmission" {
			v, ok := r["status"].(json.Number)
			if !ok {
				return nil, "", ErrDependency
			}
			n, e := v.Int64()
			if e != nil || n < 0 || n > 6 {
				return nil, "", ErrDependency
			} // actual installed Status(3)=="download_pending" is false
			if n != 4 {
				continue
			}
		}
		if len(required) > 0 {
			have := map[string]bool{}
			if kind == "qbittorrent" {
				s, ok := r["tags"].(string)
				if !ok {
					return []map[string]any{}, "AttributeError", nil
				}
				for _, s := range strings.Split(s, ",") {
					have[strings.TrimSpace(s)] = true
				}
			} else {
				if _, present := r["labels"]; !present {
					return []map[string]any{}, "KeyError", nil
				}
				if r["labels"] == nil {
					return []map[string]any{}, "TypeError", nil
				}
				labels, ok := r["labels"].([]any)
				if !ok {
					return nil, "", ErrDependency
				}
				for _, v := range labels {
					s, e := sourceText(v)
					if e != nil {
						return nil, "", e
					}
					have[strings.TrimSpace(s)] = true
				}
			}
			matched := true
			for _, v := range required {
				if !have[v] {
					matched = false
					break
				}
			}
			if !matched {
				continue
			}
		}
		out = append(out, r)
	}
	return out, "", nil
}
func sourceText(v any) (string, error) {
	switch n := v.(type) {
	case string:
		return n, nil
	case json.Number:
		return string(n), nil
	case nil:
		return "None", nil
	case bool:
		if n {
			return "True", nil
		}
		return "False", nil
	}
	return "", ErrDependency
}

// TRAttributes evaluates the transmission-rpc properties consumed by the source
// module. The installed library rounds percentDone*100 to TWO places first.
func TRAttributes(r map[string]any) (map[string]any, error) {
	out := map[string]any{}
	for _, k := range []string{"name", "hashString", "labels", "status"} {
		out[k] = r[k]
	}
	v, ok := r["status"].(json.Number)
	if !ok {
		return nil, ErrDependency
	}
	n, e := v.Int64()
	if e != nil || n < 0 || n > 6 {
		return nil, ErrDependency
	}
	out["status"] = []string{"stopped", "check pending", "checking", "download pending", "downloading", "seed pending", "seeding"}[n]
	for _, pair := range [][2]string{{"downloadDir", "download_dir"}, {"totalSize", "total_size"}, {"rateDownload", "rate_download"}, {"rateUpload", "rate_upload"}, {"leftUntilDone", "left_until_done"}, {"downloadLimit", "download_limit"}, {"uploadLimit", "upload_limit"}, {"seedRatioLimit", "seed_ratio_limit"}, {"seedIdleLimit", "seed_idle_limit"}} {
		if v, ok := r[pair[0]]; ok {
			out[pair[1]] = v
		}
	}
	progress := float64(0)
	if v, ok := r["percentDone"]; ok {
		// A present None raises TypeError in both progress/percent_done and
		// the source attribute helper returns0. Only a missing key enters the
		// actual progress property's sizeWhenDone fallback.
		if v != nil {
			p, e := fallbackNumber(v)
			if e != nil {
				return nil, e
			}
			progress = Rounded(p*100, 2)
		}
	} else if s, ok := r["sizeWhenDone"]; ok {
		size, e := fallbackNumber(s)
		if e != nil {
			return nil, e
		}
		if l, present := r["leftUntilDone"]; present && size != 0 {
			left, e := fallbackNumber(l)
			if e != nil {
				return nil, e
			}
			progress = Rounded(100*(size-left)/size, 2)
		}
	}
	out["progress"] = progress
	return out, nil
}
