package cleaninvalidseed

import (
	py "light-media/internal/releasemeta/legacyanime"
	"strings"
	"time"
)

func ConvertTransmissionTrackerStatus(v map[string]any) int {
	if Truth(v["lastAnnounceSucceeded"]) && Truth(v["hasAnnounced"]) {
		return 2
	}
	if Truth(v["lastAnnounceResult"]) {
		return 4
	}
	return 1
}
func (m *Machine) TrackerInfo(t *Object, kind string) []any {
	v, e := m.trackerInfo(t, kind)
	if e != nil {
		return []any{}
	}
	return v
}
func (m *Machine) trackerInfo(t *Object, kind string) ([]any, error) {
	out := []any{}
	if kind == "qbittorrent" {
		ok, e := has(t, "trackers")
		if e != nil {
			return nil, e
		}
		if ok {
			v, e := required(t, "trackers")
			if e != nil {
				return nil, e
			}
			if Truth(v) {
				v, e = required(t, "trackers")
				if e != nil {
					return nil, e
				}
				return listValue(v)
			}
		}
		return out, nil
	}
	if kind != "transmission" {
		return out, nil
	}
	code := SafeGetTimeout(t, "error", 0, 3*time.Second)
	msg := SafeGetTimeout(t, "errorString", "", 3*time.Second)
	if !Truth(msg) {
		msg = SafeGetTimeout(t, "error_string", "", 3*time.Second)
	}
	if Truth(m.Config["more_logs"]) {
		if _, e := attr(t, "name", "Unknown"); e != nil {
			return nil, e
		}
		for _, k := range []string{"trackerStats", "trackers", "trackerList"} {
			if SafeHasTimeout(t, k, time.Second) {
				_ = SafeGetTimeout(t, k, nil, 2*time.Second)
			}
		}
		if _, e := attr(t, "name", "Unknown"); e != nil {
			return nil, e
		}
	}
	ms := ""
	if Truth(msg) {
		var e error
		ms, e = stringValue(msg, "strip")
		if e != nil {
			return nil, e
		}
	}
	if Truth(code) && !eq(code, 0) || Truth(msg) && py.Strip(ms) != "" {
		var u any = "unknown"
		if SafeHasTimeout(t, "trackerStats", 2*time.Second) {
			stats := SafeGetTimeout(t, "trackerStats", []any{}, 3*time.Second)
			if Truth(stats) {
				a, e := listValue(stats)
				if e != nil {
					return nil, e
				}
				if len(a) > 0 {
					u, e = dictGet(a[0], "announce", "unknown")
					if e != nil {
						return nil, e
					}
				}
			}
		} else if SafeHasTimeout(t, "trackers", 2*time.Second) {
			a, e := listValue(SafeGetTimeout(t, "trackers", []any{}, 3*time.Second))
			if e != nil {
				return nil, e
			}
			if len(a) > 0 {
				u = a[0]
			}
		} else if SafeHasTimeout(t, "trackerList", 2*time.Second) {
			a, e := listValue(SafeGetTimeout(t, "trackerList", []any{}, 3*time.Second))
			if e != nil {
				return nil, e
			}
			if len(a) > 0 {
				if _, ok := a[0].(string); ok {
					u = a[0]
				} else {
					u, e = dictGet(a[0], "announce", "unknown")
					if e != nil {
						return nil, e
					}
				}
			}
		}
		text := py.Strip(ms)
		if !Truth(msg) {
			text = "Error code: " + str(code)
		}
		out = append(out, map[string]any{"url": u, "status": 4, "msg": text, "tier": 0})
	}
	var stats any
	if SafeHasTimeout(t, "trackerStats", 2*time.Second) {
		stats = SafeGetTimeout(t, "trackerStats", []any{}, 3*time.Second)
	} else if SafeHasTimeout(t, "tracker_stats", 2*time.Second) {
		stats = SafeGetTimeout(t, "tracker_stats", []any{}, 3*time.Second)
	}
	if Truth(stats) {
		a, e := listValue(stats)
		if e != nil {
			return nil, e
		}
		for i, v := range a {
			item, e := m.trackerStat(v, i)
			if e == nil {
				out = append(out, item)
			}
		}
	}
	if len(out) == 0 {
		if SafeHasTimeout(t, "trackers", 2*time.Second) {
			v := SafeGetTimeout(t, "trackers", []any{}, 3*time.Second)
			if Truth(v) {
				a, e := listValue(v)
				if e != nil {
					return nil, e
				}
				for i, tv := range a {
					item, e := fallbackTracker(tv, i)
					if e != nil {
						item = map[string]any{"url": str(tv), "status": 2, "msg": "", "tier": i}
					}
					out = append(out, item)
				}
			}
		} else if SafeHasTimeout(t, "trackerList", 2*time.Second) {
			v := SafeGetTimeout(t, "trackerList", []any{}, 3*time.Second)
			if Truth(v) {
				a, e := listValue(v)
				if e != nil {
					return nil, e
				}
				for i, tv := range a {
					out = append(out, map[string]any{"url": str(tv), "status": 2, "msg": "", "tier": i})
				}
			}
		}
	}
	return out, nil
}
func (m *Machine) trackerStat(v any, i int) (map[string]any, error) {
	if d, ok := v.(map[string]any); ok {
		u := d["announce"]
		if _, ok := d["announce"]; !ok {
			u = ""
		}
		msg := d["lastAnnounceResult"]
		if _, ok := d["lastAnnounceResult"]; !ok {
			msg = ""
		}
		tier := d["tier"]
		if _, ok := d["tier"]; !ok {
			tier = 0
		}
		return map[string]any{"url": u, "status": ConvertTransmissionTrackerStatus(d), "msg": msg, "tier": tier}, nil
	}
	u, e := attr(v, "announce", "")
	if e != nil {
		return nil, e
	}
	if !Truth(u) {
		u, e = attr(v, "announceUrl", "")
		if e != nil {
			return nil, e
		}
	}
	msg, e := attr(v, "last_announce_result", "")
	if e != nil {
		return nil, e
	}
	if !Truth(msg) {
		msg, e = attr(v, "lastAnnounceResult", "")
		if e != nil {
			return nil, e
		}
	}
	success, e := attr(v, "last_announce_succeeded", true)
	if e != nil {
		return nil, e
	}
	if success == nil {
		success, e = attr(v, "lastAnnounceSucceeded", true)
		if e != nil {
			return nil, e
		}
	}
	tier, e := attr(v, "tier", i)
	if e != nil {
		return nil, e
	}
	errorMsg := false
	if Truth(msg) {
		msgs, e := m.customErrors()
		if e != nil {
			return nil, e
		}
		errorMsg = contains(msgs, msg)
	}
	status := 1
	if !Truth(success) || errorMsg {
		status = 4
	} else if Truth(success) && eq(msg, "Success") {
		status = 2
	}
	return map[string]any{"url": u, "status": status, "msg": msg, "tier": tier}, nil
}
func fallbackTracker(v any, i int) (map[string]any, error) {
	u, e := attr(v, "announce", str(v))
	if e != nil {
		return nil, e
	}
	msg, e := attr(v, "lastAnnounceResult", "")
	if e != nil {
		return nil, e
	}
	success, e := attr(v, "lastAnnounceSucceeded", true)
	if e != nil {
		return nil, e
	}
	status := 1
	if Truth(msg) && !Truth(success) {
		status = 4
	} else if Truth(success) {
		status = 2
	}
	return map[string]any{"url": u, "status": status, "msg": msg, "tier": i}, nil
}
func Paused(t *Object, kind string) (bool, error) {
	if kind == "qbittorrent" {
		ok, e := has(t, "state_enum")
		if e != nil || !ok {
			return false, e
		}
		v, e := required(t, "state_enum")
		if e != nil {
			return false, e
		}
		p, e := required(v, "is_paused")
		return Truth(p), e
	}
	if kind == "transmission" {
		ok, e := has(t, "status")
		if e != nil || !ok {
			return false, e
		}
		v, e := required(t, "status")
		return py.LowerCase(str(v)) == "stopped", e
	}
	return false, nil
}
func Category(t *Object, kind string) (any, error) {
	if kind == "qbittorrent" {
		return attr(t, "category", "")
	}
	return "", nil
}
func Tags(t *Object, kind string) (any, error) {
	if kind == "qbittorrent" {
		return attr(t, "tags", "")
	}
	if kind == "transmission" {
		v, e := attr(t, "labels", []any{})
		if e != nil {
			return nil, e
		}
		if Truth(v) {
			a, e := listValue(v)
			if e != nil {
				return nil, e
			}
			s := []string{}
			for _, v := range a {
				s = append(s, str(v))
			}
			return strings.Join(s, ","), nil
		}
	}
	return "", nil
}
func Hash(t *Object, kind string) (any, error) {
	if kind == "qbittorrent" {
		if t.Mapping {
			return t.Attrs["hash"], nil
		}
		return attr(t, "hash", nil)
	}
	if kind == "transmission" {
		return attr(t, "hashString", nil)
	}
	return nil, nil
}
