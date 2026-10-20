package web

import (
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"testing"
	"testing/fstest"
)

type deniedVersionFS struct{}

func (deniedVersionFS) Open(string) (fs.File, error) { return nil, fs.ErrPermission }
func TestFrontendBuildVersionUsesRealManifestAndSourceFile(t *testing.T) {
	var metadata struct{ Version string }
	if json.Unmarshal(buildManifest, &metadata) != nil || metadata.Version == "" {
		t.Fatal("real frontend build source absent")
	}
	got, e := FrontendVersion()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = fs.Stat(files, "dist/version.txt"); e != nil && got != metadata.Version {
		t.Fatal("missing version.txt did not use actual package build", got, metadata.Version)
	}
	raw, e := os.ReadFile("../internal/systemversion/testdata/original.json")
	if e != nil {
		t.Fatal(e)
	}
	var oracle struct {
		Cases []struct {
			Name  string
			Input struct {
				Exists    bool
				FileHex   string `json:"file_hex"`
				ReadError bool   `json:"read_error"`
			}
			Fallback, Version string
		} `json:"frontend_cases"`
	}
	if json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 9 {
		t.Fatal("source frontend oracle absent")
	}
	for _, tc := range oracle.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			var input fs.FS = fstest.MapFS{}
			if tc.Input.ReadError {
				input = deniedVersionFS{}
			} else if tc.Input.Exists {
				body, e := hex.DecodeString(tc.Input.FileHex)
				if e != nil {
					t.Fatal(e)
				}
				input = fstest.MapFS{"dist/version.txt": {Data: body}}
			}
			if got := frontendVersion(input, tc.Fallback); got != tc.Version {
				t.Fatal("original file/fallback behavior changed", got, tc.Version)
			}
		})
	}
}
