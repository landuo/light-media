package directorysource

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
)

func TestInstalledDirectoryHelperAndDefaultsAST(t *testing.T) {
	b, e := os.ReadFile("testdata/source-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixture struct {
		Normalize []struct {
			Input json.RawMessage
			Value json.RawMessage
			Error string
		}
		Cases []struct {
			Method              string
			Rules, Query, Value json.RawMessage
			SameDisk            json.RawMessage `json:"same_disk"`
			Error               string
		}
	}
	if e = json.Unmarshal(b, &fixture); e != nil {
		t.Fatal(e)
	}
	equal := func(a any, b json.RawMessage) bool {
		encoded, _ := json.Marshal(a)
		x, e1 := Decode(encoded)
		y, e2 := Decode(b)
		return e1 == nil && e2 == nil && reflect.DeepEqual(x, y)
	}
	errorKind := func(e error) string {
		var source *SourceError
		if errors.As(e, &source) {
			return source.Kind
		}
		if e != nil {
			return e.Error()
		}
		return ""
	}
	for i, c := range fixture.Normalize {
		t.Run("normalize/"+testNumber(i), func(t *testing.T) {
			input, e := Decode(c.Input)
			if e != nil {
				t.Fatal(e)
			}
			r, e := Normalize(Object(input.(map[string]any)))
			if errorKind(e) != c.Error {
				t.Fatal("source validation mismatch", string(c.Input), e, c.Error)
			}
			if e == nil && !equal(r.Values, c.Value) {
				t.Fatal("source defaults/value mismatch", string(c.Input), r.Values, string(c.Value))
			}
		})
	}
	for i, c := range fixture.Cases {
		t.Run(c.Method+"/"+testNumber(i), func(t *testing.T) {
			input, e := Decode(c.Rules)
			if e != nil {
				t.Fatal(e)
			}
			rules, e := ParseRules(input)
			var result any
			if e == nil {
				var q struct {
					Media *struct {
						Type     string
						Category *string
					}
					IncludeUnsorted                                                bool `json:"include_unsorted"`
					Storage, TargetStorage, SourcePath, DestPath, SavePath, Format *string
					Path                                                           string
				}
				// Explicit original JSON names are preserved rather than changing the oracle.
				var fields map[string]json.RawMessage
				json.Unmarshal(c.Query, &fields)
				json.Unmarshal(c.Query, &q)
				json.Unmarshal(fields["target_storage"], &q.TargetStorage)
				json.Unmarshal(fields["source_path"], &q.SourcePath)
				json.Unmarshal(fields["dest_path"], &q.DestPath)
				json.Unmarshal(fields["save_path"], &q.SavePath)
				var media *Media
				if q.Media != nil {
					media = &Media{q.Media.Type, q.Media.Category, true}
				}
				var disk [][]string
				json.Unmarshal(c.SameDisk, &disk)
				same := func(_ context.Context, a, b string) (bool, error) {
					for _, p := range disk {
						if len(p) == 2 && p[0] == a && p[1] == b {
							return true, nil
						}
					}
					return false, nil
				}
				switch c.Method {
				case "get_dir":
					var r *Rule
					r, e = GetDir(context.Background(), true, rules, Query{media, q.IncludeUnsorted, q.Storage, q.SourcePath, q.TargetStorage, q.DestPath}, same)
					if r != nil {
						result = r.Values
					}
				case "by_save_path":
					var r *Rule
					r, e = DownloadDirBySavePath(true, rules, media, q.SavePath)
					if r != nil {
						result = r.Values
					}
				case "media_root":
					var r *string
					r, e = MediaRoot(q.Format, q.Path)
					if r != nil {
						result = *r
					}
				default:
					var list []Rule
					list, e = List(true, rules, c.Method == "get_library_dirs" || c.Method == "get_local_library_dirs", c.Method == "get_local_download_dirs" || c.Method == "get_local_library_dirs")
					values := []Object{}
					for _, r := range list {
						values = append(values, r.Values)
					}
					result = values
				}
			}
			if errorKind(e) != c.Error {
				t.Fatal("source error mismatch", c.Method, string(c.Query), e, c.Error)
			}
			if e == nil && !equal(result, c.Value) {
				t.Fatal("source result mismatch", c.Method, string(c.Query), result, string(c.Value))
			}
		})
	}
}
func testNumber(n int) string { b, _ := json.Marshal(n); return string(b) }
func TestDirectoryUnknownConfigurationAndObservationRemainDependencies(t *testing.T) {
	if _, e := GetDir(context.Background(), false, nil, Query{}, nil); !errors.Is(e, ErrDependency) {
		t.Fatal(e)
	}
	if _, e := List(false, nil, false, false); !errors.Is(e, ErrDependency) {
		t.Fatal(e)
	}
	if _, e := DownloadDirBySavePath(false, nil, nil, nil); !errors.Is(e, ErrDependency) {
		t.Fatal(e)
	}
	r, e := Normalize(Object{"download_path": "/owned", "storage": "local", "library_storage": "local", "monitor_type": "monitor"})
	if e != nil {
		t.Fatal(e)
	}
	p := "/owned/file"
	if _, e = GetDir(context.Background(), true, []Rule{r}, Query{SourcePath: &p}, nil); !errors.Is(e, ErrDependency) {
		t.Fatal(e)
	}
}
