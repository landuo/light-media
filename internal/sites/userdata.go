package sites

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"light-media/internal/logging"
)

// SourceUserData is the MoviePilot DTO, distinct from observed native values.
// Its constructor defaults are protocol values, never evidence of measured zero.
type SourceUserData struct {
	Domain        string     `json:"domain"`
	Username      *string    `json:"username"`
	UserID        any        `json:"userid"`
	UserLevel     *string    `json:"user_level"`
	JoinAt        *string    `json:"join_at"`
	Bonus         float64    `json:"bonus"`
	Upload        int64      `json:"upload"`
	Download      int64      `json:"download"`
	Ratio         float64    `json:"ratio"`
	Seeding       int64      `json:"seeding"`
	Leeching      int64      `json:"leeching"`
	SeedingSize   int64      `json:"seeding_size"`
	LeechingSize  int64      `json:"leeching_size"`
	SeedingInfo   [][2]int64 `json:"seeding_info"`
	MessageUnread int64      `json:"message_unread"`
	Messages      [][3]any   `json:"message_unread_contents"`
	Error         *string    `json:"err_msg"`
	Day           string     `json:"updated_day"`
	Time          *string    `json:"updated_time"`
}

// Private snapshots must not round an original integer ID or arbitrary JSON
// message values through float64 when reopened.
func (d *SourceUserData) UnmarshalJSON(raw []byte) error {
	type plain SourceUserData
	var value plain
	if !decodeJSON(raw, &value) {
		return sourceModuleError(nil)
	}
	*d = SourceUserData(value)
	return nil
}

type sourceUserStatsKey struct{}
type sourceUserStatsTrace struct {
	guardError  error
	status      int
	contentType string
}

func sourceStatsTrace(ctx context.Context) *sourceUserStatsTrace {
	v, _ := ctx.Value(sourceUserStatsKey{}).(*sourceUserStatsTrace)
	return v
}

// SourceUserStats preserves the module's three outcomes: a DTO (possibly with
// err_msg), no DTO after an unsupported/throwing parser, or a revoked operation.
// The error alongside a DTO describes native connection health, not DTO truth.
func (c *Client) SourceUserStats(ctx context.Context, options UserStatsOptions) (UserStats, *SourceUserData, error) {
	trace := &sourceUserStatsTrace{}
	ctx = context.WithValue(ctx, sourceUserStatsKey{}, trace)
	ctx = logging.SiteContext(ctx, c.config.ID)
	ctx, _ = logging.Target(ctx, "site")
	var s UserStats
	var d *SourceUserData
	var e error
	if c.config.Kind == "mteam" {
		s, d, e = c.sourceMTeamUserStats(ctx, options)
	} else {
		at := time.Now()
		if options.Location != nil {
			at = at.In(options.Location)
		}
		s, d, e = c.sourceNexusUserStats(ctx, options, at)
	}
	if ctx.Err() != nil {
		return s, nil, ctx.Err()
	}
	if trace.guardError != nil {
		return s, nil, trace.guardError
	}
	return s, d, e
}

func sourceModuleError(err error) error {
	if err != nil {
		return err
	}
	return errors.New("site_user_source_module_failed")
}

func emptySourceUserData() *SourceUserData {
	return &SourceUserData{SeedingInfo: [][2]int64{}, Messages: [][3]any{}}
}

func sourceMTeamProfile(dto *SourceUserData, root map[string]any) error {
	base, err := sourceMTeamBase(root)
	if err != nil {
		return err
	}
	data := base.data
	stringField := func(v any) (*string, bool) {
		if v == nil {
			return nil, true
		}
		s, ok := v.(string)
		return &s, ok
	}
	var valid bool
	dto.Username, valid = stringField(data["username"])
	if !valid {
		return sourceModuleError(nil)
	}
	switch id := data["id"].(type) {
	case nil, string:
		dto.UserID = id
	case bool:
		if id {
			dto.UserID = int64(1)
		} else {
			dto.UserID = int64(0)
		}
	case json.Number:
		if strings.ContainsAny(string(id), ".eE") {
			f, e := id.Float64()
			if e != nil || math.IsNaN(f) || math.IsInf(f, 0) || math.Trunc(f) != f {
				return sourceModuleError(nil)
			}
		}
		n, e := sourceInteger(id)
		if e != nil {
			return sourceModuleError(nil)
		}
		if n.IsInt64() {
			dto.UserID = n.Int64()
		} else {
			dto.UserID = json.Number(n.String())
		}
	default:
		return sourceModuleError(nil)
	}
	dto.UserLevel = base.role
	dto.JoinAt, valid = stringField(base.joinAt)
	if !valid {
		return sourceModuleError(nil)
	}
	dto.Upload, dto.Download = base.uploaded, base.downloaded
	var e error
	if dto.Bonus, e = sourceFloat(base.counts["bonus"]); e != nil {
		return e
	}
	if dto.Ratio, e = sourceFloat(base.counts["shareRate"]); e != nil {
		return e
	}
	return nil
}

func sourceUserInteger(v any) (int64, error) {
	if !sourceTruth(v) {
		return 0, nil
	}
	n, e := sourceInteger(v)
	if e == nil && n.IsInt64() {
		return n.Int64(), nil
	}
	return 0, sourceModuleError(nil)
}

func sourceFloat(v any) (float64, error) {
	if !sourceTruth(v) {
		return 0, nil
	}
	if b, ok := v.(bool); ok && b {
		return 1, nil
	}
	f, e := strconv.ParseFloat(stringValue(v), 64)
	if e != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, sourceModuleError(nil)
	}
	return f, nil
}
