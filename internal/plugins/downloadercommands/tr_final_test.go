package downloadercommands

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"testing"
)

//go:embed testdata/source-tr-final.json
var sourceTRFinal []byte

type trFinalStep struct {
	Method string `json:"method"`
	Reply  any    `json:"reply"`
	Raises *struct {
		Class   string `json:"class"`
		Message string `json:"message"`
	} `json:"raises"`
}
type trFinalCase struct {
	ID    string `json:"id"`
	Input struct {
		Method        string         `json:"method"`
		Args          map[string]any `json:"args"`
		ClientPresent bool           `json:"client_present"`
		Steps         []trFinalStep  `json:"steps"`
	} `json:"input"`
	Outcome        map[string]any `json:"outcome"`
	Trace          []any          `json:"trace"`
	Calls          int            `json:"recording_client_calls"`
	ArgumentsAfter map[string]any `json:"arguments_after_source"`
}

func trFinalObserved(v any) any {
	if f, ok := v.(float64); ok {
		s := strconv.FormatFloat(f, 'g', -1, 64)
		if !strings.ContainsAny(s, ".eE") {
			s += ".0"
		}
		return json.Number(s)
	}
	return v
}

func trFinalCallFields(c TRChangeCall) map[string]any {
	x := map[string]any{"ids": c.IDs}
	if c.Labels != nil {
		v := []any{}
		for _, s := range *c.Labels {
			v = append(v, s)
		}
		x["labels"] = v
	}
	if c.UploadLimited != nil {
		x["uploadLimited"] = *c.UploadLimited
	}
	if c.UploadLimit != nil {
		x["uploadLimit"] = *c.UploadLimit
	}
	if c.DownloadLimited != nil {
		x["downloadLimited"] = *c.DownloadLimited
	}
	if c.DownloadLimit != nil {
		x["downloadLimit"] = *c.DownloadLimit
	}
	if c.SeedRatioMode != nil {
		x["seedRatioMode"] = *c.SeedRatioMode
	}
	if c.SeedRatioLimit != nil {
		x["seedRatioLimit"] = c.SeedRatioLimit
	}
	if c.SeedIdleMode != nil {
		x["seedIdleMode"] = *c.SeedIdleMode
	}
	if c.SeedIdleLimit != nil {
		x["seedIdleLimit"] = *c.SeedIdleLimit
	}
	return x
}

type trFinalRecording struct {
	recordingTR
	t      *testing.T
	steps  []trFinalStep
	trace  []any
	calls  int
	forced error
	cancel context.CancelFunc
	last   TRChangeCall
}

func (p *trFinalRecording) log(_ context.Context, level, message string) {
	p.trace = append(p.trace, map[string]any{"event": "log", "level": level, "message": message})
}
func (p *trFinalRecording) Change(_ context.Context, c TRChangeCall) (any, error) {
	p.t.Helper()
	p.last = c
	p.calls++
	x := trFinalCallFields(c)
	types := map[string]any{}
	for k, v := range x {
		types[k] = qbTagType(v)
		x[k] = trFinalObserved(v)
	}
	p.trace = append(p.trace, map[string]any{"event": "recording_client_call", "method": "change_torrent", "ordinal": p.calls, "kwargs": x, "python_types": types})
	if p.cancel != nil {
		p.cancel()
	}
	if p.forced != nil {
		return nil, p.forced
	}
	if p.calls > len(p.steps) {
		p.t.Fatal("unexpected fixed change/replay")
	}
	s := p.steps[p.calls-1]
	if s.Method != "change_torrent" {
		p.t.Fatal("unplanned method")
	}
	if s.Raises != nil {
		p.trace = append(p.trace, map[string]any{"event": "recording_client_exception", "method": s.Method, "class": s.Raises.Class, "message": s.Raises.Message})
		return nil, &SourceException{s.Raises.Class, s.Raises.Message}
	}
	p.trace = append(p.trace, map[string]any{"event": "recording_client_return", "method": s.Method, "value": s.Reply})
	return s.Reply, nil
}
func trFinalFixture(t *testing.T) (Transmission, *trFinalRecording) {
	p := &trFinalRecording{recordingTR: recordingTR{&recordingPort{t: t, method: "old ports forbidden"}}, t: t, trace: []any{}}
	return Transmission{Client: p, Log: p.log}, p
}

// Normalize only labels in recorded change_torrent kwargs, on fresh copies.
// Original raw remains byte-bound and unmodified. All other trace order, ids,
// types, logs, results and input arrays stay exact.
func trFinalTraceProjection(t *testing.T, trace []any) []any {
	t.Helper()
	out := make([]any, 0, len(trace))
	for _, item := range trace {
		row := item.(map[string]any)
		copyRow := map[string]any{}
		for k, v := range row {
			copyRow[k] = v
		}
		if kwargs, ok := row["kwargs"].(map[string]any); ok {
			copyKw := map[string]any{}
			for k, v := range kwargs {
				copyKw[k] = v
			}
			if labels, ok := kwargs["labels"].([]any); ok {
				stringsOnly := []string{}
				seen := map[string]bool{}
				for _, v := range labels {
					s, ok := v.(string)
					if !ok || seen[s] {
						t.Fatal("not unique string-label set")
					}
					seen[s] = true
					stringsOnly = append(stringsOnly, s)
				}
				sort.Strings(stringsOnly)
				copyKw["labels"] = stringsOnly
			}
			copyRow["kwargs"] = copyKw
		}
		out = append(out, copyRow)
	}
	return out
}

func TestTRFinalLeafActualSourceObservations(t *testing.T) {
	h := sha256.Sum256(sourceTRFinal)
	if hex.EncodeToString(h[:]) != "1c3a9652c19898ed1aa98d63b1650f07587dee87654384490047e7872f62671e" {
		t.Fatal("not complete original raw23")
	}
	var oracle struct {
		SourceCommit string        `json:"source_commit"`
		CaseCount    int           `json:"case_count"`
		Cases        []trFinalCase `json:"cases"`
	}
	d := json.NewDecoder(bytes.NewReader(sourceTRFinal))
	d.UseNumber()
	if err := d.Decode(&oracle); err != nil {
		t.Fatal(err)
	}
	if oracle.SourceCommit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || oracle.CaseCount != 23 || len(oracle.Cases) != 23 {
		t.Fatal("source scope")
	}
	trueCount, falseCount, errorCount, calls, logs := 0, 0, 0, 0, 0
	for _, c := range oracle.Cases {
		t.Run(c.ID, func(t *testing.T) {
			tr, p := trFinalFixture(t)
			p.steps = c.Input.Steps
			if !c.Input.ClientPresent {
				tr.Client = nil
			}
			x := c.Input.Args
			var result bool
			var err error
			switch c.Input.Method {
			case "set_torrent_tag":
				result, err = tr.SetTorrentTag(context.Background(), x["ids"], x["tags"], x["org_tags"])
			case "change_torrent":
				result, err = tr.ChangeTorrent(context.Background(), x["hash_string"], x["upload_limit"], x["download_limit"], x["ratio_limit"], x["seeding_time_limit"])
			default:
				t.Fatal("unselected method")
			}
			var outcome map[string]any
			if err != nil {
				s, ok := err.(*SourceException)
				if !ok {
					t.Fatal("unexpected platform error", err)
				}
				outcome = map[string]any{"kind": "uncaught_exception", "class": s.Class, "message": s.Message}
				errorCount++
				if p.calls != 0 || len(p.trace) != 0 {
					t.Fatal("outside-catch conversion became dependency/log")
				}
			} else {
				outcome = map[string]any{"kind": "return", "value": result, "python_type": "bool", "python_truthy": result}
				if result {
					trueCount++
				} else {
					falseCount++
				}
			}
			sameJSON(t, "source outcome", outcome, c.Outcome)
			// Preserve both run-local permutations in the actual verbose test
			// evidence before the one declared label-set comparison projection.
			for index, item := range p.trace {
				row := item.(map[string]any)
				kwargs, ok := row["kwargs"].(map[string]any)
				if !ok || kwargs["labels"] == nil {
					continue
				}
				if index >= len(c.Trace) {
					t.Fatal("target label call has no source trace position")
				}
				sourceRow := c.Trace[index].(map[string]any)
				sourceKw, ok := sourceRow["kwargs"].(map[string]any)
				if !ok || sourceKw["labels"] == nil {
					t.Fatal("target label call differs from source trace position")
				}
				sourceSequence, err := json.Marshal(sourceKw["labels"])
				if err != nil {
					t.Fatal(err)
				}
				targetSequence, err := json.Marshal(kwargs["labels"])
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("label_set_order case=%s source_sequence=%s target_sequence=%s", c.ID, sourceSequence, targetSequence)
			}
			sameJSON(t, "full trace, string label set projection only", trFinalTraceProjection(t, p.trace), trFinalTraceProjection(t, c.Trace))
			sameJSON(t, "caller arguments after source", x, c.ArgumentsAfter)
			if p.calls != c.Calls || p.calls != len(p.steps) {
				t.Fatal("fixed port call count")
			}
			calls += p.calls
			for _, row := range p.trace {
				if row.(map[string]any)["event"] == "log" {
					logs++
				}
			}
		})
	}
	if trueCount != 9 || falseCount != 10 || errorCount != 4 || calls != 12 || logs != 6 {
		t.Fatal("source23 summary changed", trueCount, falseCount, errorCount, calls, logs)
	}
}

func TestTRFinalLeafBoundariesAndOwnership(t *testing.T) {
	ctx := context.Background()
	for _, tag := range []bool{true, false} {
		tr, p := trFinalFixture(t)
		wrapped := fmt.Errorf("owned unknown: %w", &SourceException{"ValueError", "not safe"})
		p.forced = wrapped
		var err error
		if tag {
			_, err = tr.SetTorrentTag(ctx, "owned", []any{"one"}, nil)
		} else {
			_, err = tr.ChangeTorrent(ctx, "owned", 1, nil, nil, nil)
		}
		if !errors.Is(err, wrapped) || p.calls != 1 || len(p.trace) != 1 {
			t.Fatal("wrapped boundary swallowed", err, p.trace)
		}
	}
	tr, p := trFinalFixture(t)
	tr.Client = nil
	if _, err := tr.ChangeTorrent(ctx, "owned", "owned-invalid", nil, nil, nil); err == nil || len(p.trace) != 0 {
		t.Fatal("missing client swallowed preparation error")
	}
	tr, p = trFinalFixture(t)
	for _, v := range []any{json.Number("9223372036854775808"), float64(0x1p63), []any{1}, "１２", []byte("1")} {
		_, err := tr.ChangeTorrent(ctx, "owned", v, nil, nil, nil)
		var b *BoundaryError
		if !errors.As(err, &b) || p.calls != 0 {
			t.Fatal("domain overflow/type was not explicit boundary", v, err)
		}
	}
	if _, err := tr.ChangeTorrent(nil, "owned", 1, nil, nil, nil); err == nil || p.calls != 0 {
		t.Fatal("nil context")
	}
	tr.Client = recordingTR{&recordingPort{t: t, method: "old ports forbidden"}}
	if _, err := tr.ChangeTorrent(ctx, "owned", 1, nil, nil, nil); err == nil {
		t.Fatal("missing optional port")
	}
	tr, p = trFinalFixture(t)
	p.steps = []trFinalStep{{Method: "change_torrent"}}
	if ok, err := tr.ChangeTorrent(ctx, "owned", json.Number("1e-400"), -3.9, "-0", nil); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if *p.last.UploadLimited || *p.last.UploadLimit != 0 || *p.last.DownloadLimit != -3 || *p.last.SeedRatioMode != 1 {
		t.Fatal("binary64 truth/truncation or string-before-conversion")
	}
	f, ok := p.last.SeedRatioLimit.(float64)
	if !ok || f != 0 || !math.Signbit(f) {
		t.Fatal("truthy ratio float negative zero erased")
	}
	tr, p = trFinalFixture(t)
	cancelCtx, cancel := context.WithCancel(ctx)
	p.cancel = cancel
	p.steps = []trFinalStep{{Method: "change_torrent"}}
	if _, err := tr.ChangeTorrent(cancelCtx, "owned", 1, nil, nil, nil); !errors.Is(err, context.Canceled) || p.calls != 1 {
		t.Fatal("cancel lost", err)
	}
	tr, p = trFinalFixture(t)
	original := []any{"old", "old"}
	added := []any{"new", "old"}
	p.steps = []trFinalStep{{Method: "change_torrent"}}
	if ok, err := tr.SetTorrentTag(ctx, "owned", added, original); err != nil || !ok {
		t.Fatal(ok, err)
	}
	(*p.last.Labels)[0] = "changed-native-copy"
	sameJSON(t, "old labels not aliased", original, []any{"old", "old"})
	sameJSON(t, "new labels not aliased", added, []any{"new", "old"})
}
