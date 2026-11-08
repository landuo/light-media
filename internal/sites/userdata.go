package sites

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"light-media/internal/logging"
	"light-media/internal/releasemeta/legacyanime"
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
	// Messages preserves every source-supplied array element. Parsers that provide
	// only heading/date/body continue to serialize exactly three values; an optional
	// fourth source and later metadata must survive private snapshot roundtrips.
	Messages [][]any `json:"message_unread_contents"`
	// JSON-decoded snapshots keep authoritative raw rows, including nested object
	// key order. Messages remains a compatibility view for all-array lists.
	// Parser-created DTOs have no overlay until serialized and reopened; decoded
	// snapshots are immutable, so modifying Messages does not replace raw authority.
	rawMessageRows []json.RawMessage
	rawSeedingInfo json.RawMessage
	rawIntegers    map[string]json.RawMessage
	nullFields     map[string]bool
	Error          *string `json:"err_msg"`
	Day            string  `json:"updated_day"`
	Time           *string `json:"updated_time"`
}

// Private snapshots must not round an original integer ID or arbitrary JSON
// message values through float64 when reopened.
func (d *SourceUserData) UnmarshalJSON(raw []byte) error {
	type plain SourceUserData
	var value plain
	wire := struct {
		*plain
		Rows    json.RawMessage `json:"message_unread_contents"`
		Seeding json.RawMessage `json:"seeding_info"`
	}{plain: &value}
	compatible, wide, err := sourceUserDataIntegerView(raw)
	if err != nil {
		return err
	}
	if !decodeJSON(compatible, &wire) {
		return sourceModuleError(nil)
	}
	value.rawIntegers = wide
	var fields map[string]json.RawMessage
	if !decodeJSON(raw, &fields) {
		return sourceModuleError(nil)
	}
	if len(wire.Seeding) > 0 {
		var entries []json.RawMessage
		if !decodeJSON(wire.Seeding, &entries) {
			return sourceModuleError(nil)
		}
		value.rawSeedingInfo = append(json.RawMessage(nil), wire.Seeding...)
		_ = decodeJSON(wire.Seeding, &value.SeedingInfo)
	}
	for key, token := range fields {
		if string(token) == "null" {
			if value.nullFields == nil {
				value.nullFields = map[string]bool{}
			}
			value.nullFields[key] = true
		}
	}
	var rows []json.RawMessage
	if len(wire.Rows) != 0 && !decodeJSON(wire.Rows, &rows) {
		return sourceModuleError(nil)
	}
	if rows != nil {
		value.rawMessageRows = rows
		value.Messages = make([][]any, len(rows))
		for i, row := range rows {
			var array []any
			if len(row) == 0 || row[0] != '[' || !decodeJSON(row, &array) {
				value.Messages = nil
				break
			}
			value.Messages[i] = array
		}
	}
	*d = SourceUserData(value)
	return nil
}

func (d SourceUserData) MarshalJSON() ([]byte, error) {
	type plain SourceUserData
	raw, err := json.Marshal(plain(d))
	if err != nil {
		return nil, err
	}
	if d.rawMessageRows == nil && len(d.nullFields) == 0 && len(d.rawSeedingInfo) == 0 && len(d.rawIntegers) == 0 {
		return raw, nil
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	if d.rawMessageRows != nil {
		fields["message_unread_contents"], err = json.Marshal(d.rawMessageRows)
		if err != nil {
			return nil, err
		}
	}
	if len(d.rawSeedingInfo) > 0 {
		fields["seeding_info"] = d.rawSeedingInfo
	}
	for key, raw := range d.rawIntegers {
		fields[key] = raw
	}
	for key := range d.nullFields {
		value := string(fields[key])
		if value == "0" || value == `""` || value == "null" {
			fields[key] = json.RawMessage(`null`)
		}
	}
	return json.Marshal(fields)
}

// SourceFieldNull distinguishes a nullable source column from an observed
// numeric zero when projecting an actual module DTO into native account views.
func (d SourceUserData) SourceFieldNull(key string) bool { return d.nullFields[key] }

// SourceMessageRows preserves list entries even when a source row cannot be
// unpacked. The producer must examine entries in order, rather than rejecting a
// later invalid row before earlier message side effects and the collected fact.
func (d SourceUserData) SourceMessageRows() []any {
	if d.rawMessageRows != nil {
		rows := make([]any, len(d.rawMessageRows))
		for i, row := range d.rawMessageRows {
			rows[i] = row
		}
		return rows
	}
	if d.Messages == nil {
		return nil
	}
	rows := make([]any, len(d.Messages))
	for i, row := range d.Messages {
		rows[i] = row
	}
	return rows
}

type sourceUserStatsKey struct{}
type sourceUserStatsTrace struct {
	parserFailure *sourceUserParserFailure
	guardError    error
	status        int
	contentType   string
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
		if c.config.Kind == "rousi" {
			s, d, e = c.sourceRousiUserStats(ctx, options, at)
		} else {
			s, d, e = c.sourceNexusUserStats(ctx, options, at)
		}
	}
	if ctx.Err() != nil {
		return s, nil, ctx.Err()
	}
	if trace.guardError != nil {
		return s, nil, &sourceUserRevokedError{trace.guardError}
	}
	if marker, ok := e.(*sourceUserParserFailure); ok && d == nil && marker == trace.parserFailure {
		e = &sourceUserModuleFailure{kind: marker.kind, stack: marker.stack}
	}
	return s, d, e
}

// SourceUserModuleFailureKind classifies only a fresh SourceUserStats module
// failure. It intentionally does not unwrap: a guard may return an older failure,
// and wrapping or reusing one is not evidence of another parser execution.
func SourceUserModuleFailureKind(err error) (string, bool) {
	failure, ok := err.(*sourceUserModuleFailure)
	if !ok || failure == nil {
		return "", false
	}
	return failure.kind, true
}

// SourceUserModuleFailureDiagnostic exposes only an audited fresh failure and
// Go function names/line numbers, never response bodies, credentials or paths.
func SourceUserModuleFailureDiagnostic(err error) (kind, stack string, ok bool) {
	failure, ok := err.(*sourceUserModuleFailure)
	if !ok || failure == nil || failure.stack == "" {
		return "", "", false
	}
	return failure.kind, failure.stack, true
}

type sourceUserModuleFailure struct{ kind, stack string }

func (*sourceUserModuleFailure) Error() string { return "site_user_source_module_failed" }

type sourceUserParserFailure struct{ kind, stack string }

func (*sourceUserParserFailure) Error() string { return "site_user_source_module_failed" }

// Registration occurs only immediately after an audited parser branch, never
// by searching an arbitrary returned error chain for a matching type or text.
func recordSourceUserParserFailure(ctx context.Context, err error) error {
	if marker, ok := err.(*sourceUserParserFailure); ok {
		if trace := sourceStatsTrace(ctx); trace != nil {
			pcs := make([]uintptr, 32)
			n := runtime.Callers(2, pcs)
			frames := runtime.CallersFrames(pcs[:n])
			var lines strings.Builder
			lines.WriteString("Go native parser diagnostic (not Python traceback):\n")
			for {
				frame, more := frames.Next()
				if strings.HasPrefix(frame.Function, "light-media/internal/sites.") {
					fmt.Fprintf(&lines, "%s:%d\n", frame.Function, frame.Line)
				}
				if !more {
					break
				}
			}
			marker.stack = lines.String()
			trace.parserFailure = marker
		}
	}
	return err
}

type sourceUserRevokedError struct{ error }

func (e *sourceUserRevokedError) Unwrap() error { return e.error }

type sourceUserIntegerFormatError struct{}

func (*sourceUserIntegerFormatError) Error() string { return "site_user_source_module_failed" }

func sourceModuleError(err error) error {
	if err != nil {
		return err
	}
	return errors.New("site_user_source_module_failed")
}

func emptySourceUserData() *SourceUserData {
	return &SourceUserData{SeedingInfo: [][2]int64{}, Messages: [][]any{}}
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

var sourceUserDecimalDigits = regexp.MustCompile(legacyanime.DecimalClass())

func sourceUserInteger(v any) (int64, error) {
	if !sourceTruth(v) {
		return 0, nil
	}
	n, e := sourceInteger(v)
	if e != nil {
		// The audited case is a malformed string conversion. Numeric overflow,
		// non-string schema errors, and conversion size limits are not promoted.
		if value, ok := v.(string); ok && len(sourceUserDecimalDigits.FindAllStringIndex(value, 4301)) <= 4300 {
			return 0, &sourceUserIntegerFormatError{}
		}
		return 0, sourceModuleError(nil)
	}
	if n.IsInt64() {
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
