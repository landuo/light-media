package removelink

import (
	"encoding/json"
	"os"
	"os/user"
	"reflect"
	"strings"
	"testing"
)

func TestSource312UnknownNamedHomeStopsBeforeUnlink(t *testing.T) {
	if _, err := user.Lookup("__lm_review_no_such_user_20261106__"); err == nil {
		t.Fatal("synthetic source username unexpectedly exists")
	}
	raw, err := os.ReadFile("reference/unknown-home-source312.json")
	if err != nil {
		t.Fatal(err)
	}
	var suite struct {
		Source string `json:"source_sha256"`
		Cases  []struct {
			Input    oracleCase
			Expected map[string]any
		}
	}
	if json.Unmarshal(raw, &suite) != nil || suite.Source != "22fb375e531378b3454607c7bca305b637abd60ee8ed69e14e32fbc2ddfc5485" || len(suite.Cases) != 5 {
		t.Fatal("unattested fixed source unknown-home oracle")
	}
	for _, row := range suite.Cases {
		t.Run(row.Input.Name, func(t *testing.T) {
			got := runOracle(t, row.Input).(map[string]any)
			for _, key := range []string{"files", "state", "queue", "trace", "delay", "enabled", "error", "logged_errors"} {
				a, _ := json.Marshal(got[key])
				b, _ := json.Marshal(row.Expected[key])
				if string(a) != string(b) {
					t.Errorf("source %s=%s; Go=%s", key, b, a)
				}
			}
			logs, _ := json.Marshal(runSourceLogOracle(t, row.Input).(map[string]any)["logs"])
			var logged [][2]string
			if err := json.Unmarshal(logs, &logged); err != nil {
				t.Fatal(err)
			}
			errors := 0
			for _, entry := range logged {
				if entry[0] == "error" {
					errors++
					expectedPrefix := "删除硬链接文件发生错误："
					if row.Input.Name == "unknown-home-delayed-scrap" {
						expectedPrefix = "执行延迟删除任务失败："
					}
					if !strings.HasPrefix(entry[1], expectedPrefix+"Could not determine home directory.") {
						t.Error("source RuntimeError boundary/text lost", entry)
					}
				}
			}
			if float64(errors) != row.Expected["logged_errors"] {
				t.Fatal("source catch count", errors)
			}
		})
	}
}

func TestEmptyExcludeDoesNotNormalizeUnknownHome(t *testing.T) {
	for _, exclude := range []string{"", " \n\t "} {
		if (Config{Exclude: exclude}).Excluded("~__lm_review_no_such_user_20261106__/file", "") {
			t.Fatal("source empty exclude loop unexpectedly matched")
		}
	}
}

func TestSource312PathExpansionOrder(t *testing.T) {
	raw, err := os.ReadFile("reference/unknown-home-source312.json")
	if err != nil {
		t.Fatal(err)
	}
	var suite struct {
		Normalize struct {
			Source string `json:"source_sha256"`
			Cases  []struct {
				Path  string
				Home  *string `json:"HOME"`
				Value *string
				Error *struct{ Class, Message string }
			}
			Comparisons []struct {
				Path, Base string
				Result     bool
			}
		}
	}
	if json.Unmarshal(raw, &suite) != nil || suite.Normalize.Source != "22fb375e531378b3454607c7bca305b637abd60ee8ed69e14e32fbc2ddfc5485" || len(suite.Normalize.Cases) != 17 || len(suite.Normalize.Comparisons) != 2 {
		t.Fatal("unattested normalization source addon")
	}
	for _, row := range suite.Normalize.Cases {
		t.Run(row.Path, func(t *testing.T) {
			home := ""
			if row.Home != nil {
				home = *row.Home
			}
			var out string
			err := func() (err error) { defer capture(&err); out = normalized(row.Path, home); return nil }()
			if row.Error != nil {
				if err == nil || err.Error() != row.Error.Class+":"+row.Error.Message {
					t.Fatal("source normalization error", err, row.Error)
				}
			} else if err != nil || row.Value == nil || out != *row.Value {
				t.Fatal("source normalization value", out, err, row.Value)
			}
		})
	}
	for _, row := range suite.Normalize.Comparisons {
		if got := (Config{Exclude: row.Base}).Excluded(row.Path, "//owned"); got != row.Result {
			t.Errorf("source double-root comparison %q %q =%t; Go=%t", row.Path, row.Base, row.Result, got)
		}
	}
}

func TestSource312ConstructorHomeBranches(t *testing.T) {
	mode := os.Getenv("REMOVELINK_HOME_CTOR_CASE")
	if mode == "" {
		t.Skip("requires fixed-image real UID/passwd/HOME constructor matrix; not a passed native gate")
	}
	raw, err := os.ReadFile("reference/unknown-home-source312.json")
	if err != nil {
		t.Fatal(err)
	}
	var suite struct {
		Constructor []struct {
			Mode        string
			UID         int
			Mapped      bool `json:"pwd_mapped"`
			HomePresent bool `json:"HOME_present"`
			Cases       []struct {
				Path  string
				Value *string
				Error *struct{ Class, Message string }
			}
		}
	}
	if json.Unmarshal(raw, &suite) != nil || len(suite.Constructor) != 4 {
		t.Fatal("unattested actual constructor source matrix")
	}
	for _, row := range suite.Constructor {
		if row.Mode != mode {
			continue
		}
		_, present := os.LookupEnv("HOME")
		_, currentErr := user.Current()
		if os.Getuid() != row.UID || present != row.HomePresent || (currentErr == nil) != row.Mapped {
			t.Fatal("real UID/passwd/HOME precondition mismatch", os.Getuid(), present, currentErr, row)
		}
		p := NewPlugin()
		for _, sample := range row.Cases {
			t.Run(sample.Path, func(t *testing.T) {
				var value string
				err := func() (err error) { defer capture(&err); value = normalized(sample.Path, p.machine.Home); return nil }()
				if sample.Error != nil {
					if err == nil || err.Error() != sample.Error.Class+":"+sample.Error.Message {
						t.Fatal("unmapped source home must remain unresolved", p.machine.Home, value, err, sample.Error)
					}
				} else if err != nil || sample.Value == nil || value != *sample.Value {
					t.Fatal("source constructor home", p.machine.Home, value, err, sample.Value)
				}
			})
		}
		if (Config{Exclude: ""}).Excluded("~/child", p.machine.Home) {
			t.Fatal("constructor eagerly changed empty-exclude source behavior")
		}
		t.Logf("actual mode=%s uid=%d HOME_present=%t passwd_found=%t", mode, os.Getuid(), present, row.Mapped)
		return
	}
	t.Fatal("unknown owned constructor case", mode)
}

func TestSourcePureHelperOracle(t *testing.T) {
	raw, e := os.ReadFile("reference/helpers-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	var suite struct {
		Cases []struct {
			Kind   string
			Args   []string
			Result any
			Error  any
		}
	}
	if json.Unmarshal(raw, &suite) != nil {
		t.Fatal("invalid helper oracle")
	}
	for _, x := range suite.Cases {
		t.Run(x.Kind+":"+x.Args[0], func(t *testing.T) {
			var out any
			switch x.Kind {
			case "extensions":
				out = ParseExtensions(x.Args[0])
			case "trickplay":
				out = Trickplay(x.Args[0])
			case "path":
				out = []string{cleanPath(x.Args[0]), parent(x.Args[0]), base(x.Args[0]), stem(x.Args[0]), suffix(x.Args[0])}
			case "mapping":
				a, b, ok := StoragePath(x.Args[0], x.Args[1])
				if ok {
					out = []string{a, b}
				} else {
					out = []any{nil, nil}
				}
			case "child":
				out = (Config{Exclude: x.Args[1]}).Excluded(x.Args[0], "")
			case "scrap":
				out = (Config{Extensions: ParseExtensions(x.Args[1])}).IsScrap(x.Args[0])
			default:
				t.Fatal(x.Kind)
			}
			b, _ := json.Marshal(out)
			json.Unmarshal(b, &out)
			if x.Error != nil || !reflect.DeepEqual(out, x.Result) {
				t.Fatalf("source=%v error=%v Go=%v", x.Result, x.Error, out)
			}
		})
	}
}
