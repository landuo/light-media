package autosignin

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// Independent expected logs were executed from unmodified installed AST in the
// immutable333/Python3.12.13 image, before adding these target assertions.
func TestActualSourceAdapterLog42(t *testing.T) {
	testSourceLogs(t, "reference/source-logs42.json", 42)
}
func TestActualSourceAdapterLog364(t *testing.T) {
	testSourceLogs(t, "reference/source-logs364.json", 364)
}
func TestActualSourceAdapterLog417(t *testing.T) {
	testSourceLogs(t, "reference/source-logs417.json", 417)
}
func testSourceLogs(t *testing.T, file string, count int) {
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var original struct {
		Cases []struct {
			Name, Adapter, Operation string
			Site, Input              Object
			Result                   any
			Error                    Object
			Logs                     [][]string
		}
	}
	if err = Decode(raw, &original); err != nil {
		t.Fatal(err)
	}
	if len(original.Cases) != count {
		t.Fatal("source42")
	}
	for _, c := range original.Cases {
		t.Run(c.Name, func(t *testing.T) {
			pages := &fixedPages{site: c.Site, responses: asList(clone(c.Input["responses"]))}
			logs := [][]string{}
			steps := []any{}
			entropy := []any{}
			h := SiteVisitHooks{HTTP: pages, Browser: pages, Now: func() time.Time { return oracleLocalTime(c.Input) }, Log: func(level, message string) error { logs = append(logs, []string{level, message}); return nil }}
			h.Choose = oracleChoose(c.Input, &entropy)
			oracleOCRHooks(&h, c.Site, c.Input, &steps)
			if c.Adapter == "Tjupt" {
				h.Tjupt = &TjuptDependencies{Cache: sourceLogAnswerCache{oracleAnswerCache{c.Input, &steps}}, Public: oraclePublicPages{pages}}
			}
			adapter, _ := FixedSiteAdapter(FixedAdapter(c.Adapter))
			if c.Adapter == "MTorrent" {
				adapter = MTorrentAdapter()
				h.MTeam = oracleMTeamTransport{pages}
			}
			call := adapter.Signin
			if c.Adapter == "PTZone" {
				call = PTZoneSignin
			}
			if c.Adapter == "GenericLogin" {
				call = GenericLogin
			}
			if c.Operation == "login" {
				call = adapter.Login
			}
			var result AdapterResult
			var actual any
			var err error
			if c.Adapter == "GenericSignin" {
				value := GenericSignin(context.Background(), c.Site, h)
				actual = []any{value.SiteName, value.Message, value.Success, value.LoggedIn}
			} else {
				result, err = call(context.Background(), c.Site, h)
				actual = []any{result.State, result.Message}
			}
			if c.Error != nil {
				if err == nil || err.Error() != c.Error["message"] {
					t.Fatalf("source error got %v, want %v", err, c.Error)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				sourceCheck(t, actual, c.Result)
			}
			sourceCheck(t, normalizeSourceCacheDiagnostic(logs, c.Input), normalizeSourceCacheDiagnostic(c.Logs, c.Input))
		})
	}
}

type sourceLogAnswerCache struct{ oracleAnswerCache }

func (c sourceLogAnswerCache) Read(ctx context.Context) ([]byte, error) {
	if Truth(c.input["read_error"]) {
		return nil, cacheIOError{errors.New(textValue(c.input["read_error"]))}
	}
	if raw, ok := c.input["cache"].(string); ok {
		return []byte(raw), nil
	}
	return nil, cacheIOError{os.ErrNotExist}
}
func (c sourceLogAnswerCache) Write(ctx context.Context, raw []byte) error {
	if Truth(c.input["write_error"]) {
		return cacheIOError{errors.New(textValue(c.input["write_error"]))}
	}
	return c.oracleAnswerCache.Write(ctx, raw)
}
func normalizeSourceCacheDiagnostic(rows [][]string, input Object) [][]string {
	out := make([][]string, len(rows))
	for i, row := range rows {
		out[i] = append([]string(nil), row...)
		if len(row) != 2 {
			continue
		}
		_, hasCache := input["cache"]
		// Only the source's missing-file diagnostic wording/path differs for
		// Python FileNotFoundError vs Go os.ErrNotExist. Preserve its level,
		// position, count and business prefix/suffix, and every other log.
		if !hasCache && !Truth(input["read_error"]) && strings.HasPrefix(row[1], "查询本地已知答案失败：") && (strings.Contains(row[1], "No such file or directory") || strings.Contains(row[1], "file does not exist")) {
			out[i][1] = "查询本地已知答案失败：<missing-file diagnostic>，继续请求豆瓣查询"
		}
	}
	return out
}

func TestActualSourceRemainingAdapterLog3(t *testing.T) {
	testSourceLogs(t, "reference/source-logs3-extra.json", 3)
}

func TestActualSourceModuleWrapperLog2(t *testing.T) {
	raw, err := os.ReadFile("reference/source-logs3-extra.json")
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Cases []struct {
			Name   string
			Site   Object
			Result any
			Error  Object
			Logs   [][]string
			Stats  []any
		} `json:"wrapper_cases"`
	}
	if err = Decode(raw, &source); err != nil {
		t.Fatal(err)
	}
	if len(source.Cases) != 2 {
		t.Fatal(len(source.Cases))
	}
	for _, c := range source.Cases {
		t.Run(c.Name, func(t *testing.T) {
			logs := [][]string{}
			stats := []any{}
			h := SiteVisitHooks{Log: func(level, message string) error { logs = append(logs, []string{level, message}); return nil }, Now: func() time.Time { return time.Date(2026, 11, 5, 12, 34, 56, 0, time.UTC) }, HTTP: pageTransportFunc(func(context.Context, PageRequest) (*PageResponse, error) { return nil, nil })}
			h.Stats = func(_ context.Context, domain string, success bool, seconds int) error {
				row := Object{"domain": domain, "success": success}
				if success {
					row["seconds"] = seconds
				}
				stats = append(stats, row)
				return nil
			}
			a := SiteAdapter{Match: func(string) (bool, error) { return false, errors.New("owned module match error") }}
			if c.Name == "module-login-fallback" {
				a.Match = func(string) (bool, error) { return true, nil }
				a.Signin = func(context.Context, Object, SiteVisitHooks) (AdapterResult, error) {
					return AdapterResult{false, "签到失败"}, nil
				}
				a.Login = func(context.Context, Object, SiteVisitHooks) (AdapterResult, error) {
					return AdapterResult{true, "登录成功"}, nil
				}
			}
			result, err := (SiteVisitor{Adapters: []SiteAdapter{a}, Hooks: h}).Visit(context.Background(), c.Site, "签到", true)
			if err != nil {
				t.Fatal(err)
			}
			sourceCheck(t, []any{result.SiteName, result.Message, result.Success, result.LoggedIn}, c.Result)
			sourceCheck(t, logs, c.Logs)
			sourceCheck(t, stats, c.Stats)
		})
	}
}
