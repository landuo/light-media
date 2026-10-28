package artifacts

import (
	"encoding/json"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLocalConfigurationFixedPathlibFutureOrderAndSymlinks(t *testing.T) {
	raw, err := os.ReadFile("../../../testdata/standard-market/local-paths-oracle.json")
	var oracle struct {
		Cases []struct {
			Value *string
			Root  string
			Paths []string
		}
		Filesystem []struct {
			Value string
			Paths []string
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 11 || len(oracle.Filesystem) != 4 {
		t.Fatal("fixed path oracle missing", err)
	}
	cwd, _ := os.Getwd()
	account, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	ownHome, _ := os.UserHomeDir()
	ownHome, _ = filepath.EvalSymlinks(ownHome)
	namedHome, _ := filepath.EvalSymlinks(account.HomeDir)
	for _, row := range oracle.Cases {
		actual := []string{}
		if row.Value != nil {
			for _, part := range strings.Split(*row.Value, ",") {
				if strings.TrimSpace(part) == "" {
					continue
				}
				value, err := NormalizeLocalRoot(strings.ReplaceAll(part, "${username}", account.Username))
				if err != nil {
					t.Fatal(err)
				}
				actual = append(actual, value)
			}
		}
		expected := []string{}
		for _, p := range row.Paths {
			p = strings.ReplaceAll(strings.ReplaceAll(p, "${home}", ownHome), "${named_home}", namedHome)
			if strings.HasPrefix(p, row.Root+"/") {
				p = cwd + strings.TrimPrefix(p, row.Root)
			}
			expected = append(expected, p)
		}
		if !reflect.DeepEqual(actual, expected) {
			t.Fatalf("fixed path parsing mismatch input=%v got=%q want=%q", row.Value, actual, expected)
		}
	}
	root := t.TempDir()
	if os.MkdirAll(filepath.Join(root, "target/nested"), 0700) != nil || os.Symlink("target/nested", filepath.Join(root, "link")) != nil || os.Symlink("future-parent", filepath.Join(root, "dangling")) != nil || os.WriteFile(filepath.Join(root, "regular"), []byte("owned inert file"), 0600) != nil {
		t.Fatal("cannot construct owned native path fixture")
	}
	for _, row := range oracle.Filesystem {
		// Preserve '..' until after native symlink expansion, as pathlib does.
		actual, err := NormalizeLocalRoot(root + string(filepath.Separator) + row.Value)
		expected := strings.ReplaceAll(row.Paths[0], "${root}", root)
		if err != nil || actual != expected {
			t.Fatal("native symlink/missing-parent resolution differs from actual fixed method", row.Value, actual, expected, err)
		}
	}
}
