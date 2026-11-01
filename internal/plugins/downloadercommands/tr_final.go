package downloadercommands

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"strings"

	"light-media/internal/jinja/exec"
)

// TRChangeCall is a fixed host-owned call, never an arbitrary keyword map.
// Nil members are absent, not explicit zero. Log is never a wire argument.
type TRChangeCall struct {
	IDs             any
	Log             LogFunc
	Labels          *[]string
	UploadLimited   *bool
	UploadLimit     *int64
	DownloadLimited *bool
	DownloadLimit   *int64
	SeedRatioMode   *int64
	SeedRatioLimit  any // nil, int64(0), or finite float64
	SeedIdleMode    *int64
	SeedIdleLimit   *int64
}

type TRChangePort interface {
	Change(context.Context, TRChangeCall) (any, error)
}

func trFinalCaught(ctx context.Context, log LogFunc, prefix string, err error) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if err == nil {
		return nil
	}
	// A wrapped platform/unknown error must not be swallowed because one of
	// its causes happens to be an original exception.
	if source, ok := err.(*SourceException); ok && source != nil {
		if log != nil {
			log(ctx, "ERROR", prefix+source.Message)
		}
		return nil
	}
	return err
}

func (t Transmission) trFinalCall(ctx context.Context, call TRChangeCall, prefix string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if t.Client == nil {
		return false, trFinalCaught(ctx, t.Log, prefix, &SourceException{"AttributeError", "'NoneType' object has no attribute 'change_torrent'"})
	}
	port, ok := t.Client.(TRChangePort)
	if !ok {
		return false, &BoundaryError{"configured TR client lacks the fixed change port"}
	}
	_, err := port.Change(ctx, call)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return false, trFinalCaught(ctx, t.Log, prefix, err)
	}
	return true, nil
}

func trFinalNumber(v any) (any, error) {
	n, ok := v.(json.Number)
	if !ok {
		return v, nil
	}
	if !strings.ContainsAny(string(n), ".eE") {
		i, err := strconv.ParseInt(string(n), 10, 64)
		if err != nil {
			return nil, &BoundaryError{"TR integer outside signed int64 domain"}
		}
		return i, nil
	}
	f, err := strconv.ParseFloat(string(n), 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return nil, &BoundaryError{"TR JSON number outside finite binary64 domain"}
	}
	return f, nil
}

func trFinalTruth(v any) (bool, error) {
	v, err := trFinalNumber(v)
	if err != nil {
		return false, err
	}
	return truth(v), nil
}

func (t Transmission) SetTorrentTag(ctx context.Context, ids, tags, orgTags any) (bool, error) {
	if err := fourCommandReady(ctx, ids, tags, orgTags); err != nil {
		return false, err
	}
	if t.Client == nil {
		return false, nil
	}
	idTruthy, err := trFinalTruth(ids)
	if err != nil {
		return false, err
	}
	if !idTruthy {
		return false, nil
	}
	tagsTruthy, err := trFinalTruth(tags)
	if err != nil {
		return false, err
	}
	if !tagsTruthy {
		return false, nil
	}
	labels, err := trFinalLabels(tags, orgTags)
	if err != nil {
		return false, trFinalCaught(ctx, t.Log, "设置种子标签出错：", err)
	}
	return t.trFinalCall(ctx, TRChangeCall{IDs: ids, Labels: &labels, Log: t.Log}, "设置种子标签出错：")
}

func trFinalLabels(tags, orgTags any) ([]string, error) {
	oldTruthy, err := trFinalTruth(orgTags)
	if err != nil {
		return nil, err
	}
	old := []any{}
	if oldTruthy {
		var ok bool
		old, ok = orgTags.([]any)
		if !ok {
			return nil, &BoundaryError{"TR old-label concatenation outside ordinary list domain"}
		}
	}
	next, ok := tags.([]any)
	if !ok {
		return nil, &SourceException{"TypeError", "can only concatenate list (not \"" + qbTagType(tags) + "\") to list"}
	}
	// No stable order is promised by Python string sets. Keep a private set,
	// not a sorted array or a claim of original process hash-seed order.
	set := map[string]struct{}{}
	for _, group := range [][]any{old, next} {
		for _, value := range group {
			s, ok := value.(string)
			if !ok {
				switch value.(type) {
				case []any, map[string]any:
					return nil, &SourceException{"TypeError", "unhashable type: '" + qbTagType(value) + "'"}
				default:
					return nil, &BoundaryError{"TR string-label set domain exceeded"}
				}
			}
			set[s] = struct{}{}
		}
	}
	labels := make([]string, 0, len(set))
	for label := range set {
		labels = append(labels, label)
	}
	return labels, nil
}

func trFinalInt(v any) (int64, error) {
	var err error
	v, err = trFinalNumber(v)
	if err != nil {
		return 0, err
	}
	switch x := v.(type) {
	case bool:
		if x {
			return 1, nil
		}
		return 0, nil
	case int:
		return int64(x), nil
	case int64:
		return x, nil
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) || x >= 0x1p63 || x < -0x1p63 {
			return 0, &BoundaryError{"TR integer conversion outside signed int64 domain"}
		}
		return int64(math.Trunc(x)), nil
	case string:
		s, valid := trFinalNumericString(x)
		if !valid {
			return 0, &SourceException{"ValueError", "invalid literal for int() with base 10: " + exec.PythonRepr(exec.ToValue(x))}
		}
		i, e := strconv.ParseInt(s, 10, 64)
		if e == nil {
			return i, nil
		}
		if ne, ok := e.(*strconv.NumError); ok && ne.Err == strconv.ErrRange {
			return 0, &BoundaryError{"TR integer string outside signed int64 domain"}
		}
		return 0, &SourceException{"ValueError", "invalid literal for int() with base 10: " + exec.PythonRepr(exec.ToValue(x))}
	default:
		return 0, &BoundaryError{"TR int conversion type outside declared scalar domain"}
	}
}

func trFinalFloat(v any) (float64, error) {
	var err error
	v, err = trFinalNumber(v)
	if err != nil {
		return 0, err
	}
	var f float64
	switch x := v.(type) {
	case bool:
		if x {
			f = 1
		}
	case int:
		f = float64(x)
	case int64:
		f = float64(x)
	case float64:
		f = x
	case string:
		s, valid := trFinalNumericString(x)
		if !valid {
			return 0, &SourceException{"ValueError", "could not convert string to float: " + exec.PythonRepr(exec.ToValue(x))}
		}
		// Go accepts hexadecimal float syntax, Python float(str) does not.
		if strings.ContainsAny(s, "xXpP") {
			return 0, &SourceException{"ValueError", "could not convert string to float: " + exec.PythonRepr(exec.ToValue(x))}
		}
		var e error
		f, e = strconv.ParseFloat(s, 64)
		if e != nil {
			if ne, ok := e.(*strconv.NumError); ok && ne.Err == strconv.ErrRange {
				return 0, &BoundaryError{"TR float string outside finite binary64 domain"}
			}
			return 0, &SourceException{"ValueError", "could not convert string to float: " + exec.PythonRepr(exec.ToValue(x))}
		}
	default:
		return 0, &BoundaryError{"TR float conversion type outside declared scalar domain"}
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, &BoundaryError{"TR ratio outside finite binary64 domain"}
	}
	return f, nil
}

func trFinalRound2(f float64) (float64, error) {
	// FormatFloat rounds the exact binary64 to two decimal places, ties even;
	// parsing rounds that decimal back to binary64. Multiplying by100 first
	// would introduce an extra binary rounding (not Python round(x,2)).
	r, err := strconv.ParseFloat(strconv.FormatFloat(f, 'f', 2, 64), 64)
	if err != nil || math.IsNaN(r) || math.IsInf(r, 0) {
		return 0, &BoundaryError{"TR rounded ratio outside finite binary64 domain"}
	}
	return r, nil
}

func (t Transmission) ChangeTorrent(ctx context.Context, hashString, uploadLimit, downloadLimit, ratioLimit, seedingTimeLimit any) (bool, error) {
	if err := fourCommandReady(ctx, hashString, uploadLimit, downloadLimit, ratioLimit, seedingTimeLimit); err != nil {
		return false, err
	}
	hashTruthy, err := trFinalTruth(hashString)
	if err != nil {
		return false, err
	}
	if !hashTruthy {
		return false, nil
	}
	call := TRChangeCall{IDs: hashString, Log: t.Log}
	// Preparation is outside the source call catch, in original order.
	if uploadLimit != nil {
		flag, e := trFinalTruth(uploadLimit)
		if e != nil {
			return false, e
		}
		value, e := trFinalInt(uploadLimit)
		if e != nil {
			return false, e
		}
		call.UploadLimited, call.UploadLimit = &flag, &value
	}
	if downloadLimit != nil {
		flag, e := trFinalTruth(downloadLimit)
		if e != nil {
			return false, e
		}
		value, e := trFinalInt(downloadLimit)
		if e != nil {
			return false, e
		}
		call.DownloadLimited, call.DownloadLimit = &flag, &value
	}
	if ratioLimit != nil {
		flag, e := trFinalTruth(ratioLimit)
		if e != nil {
			return false, e
		}
		mode := int64(2)
		call.SeedRatioLimit = int64(0)
		if flag {
			mode = 1
			value, e := trFinalFloat(ratioLimit)
			if e != nil {
				return false, e
			}
			value, e = trFinalRound2(value)
			if e != nil {
				return false, e
			}
			call.SeedRatioLimit = value
		}
		call.SeedRatioMode = &mode
	}
	if seedingTimeLimit != nil {
		flag, e := trFinalTruth(seedingTimeLimit)
		if e != nil {
			return false, e
		}
		mode, value := int64(2), int64(0)
		if flag {
			mode = 1
			value, e = trFinalInt(seedingTimeLimit)
			if e != nil {
				return false, e
			}
		}
		call.SeedIdleMode, call.SeedIdleLimit = &mode, &value
	}
	return t.trFinalCall(ctx, call, "设置种子出错：")
}
