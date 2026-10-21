package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func reviewedRemoveLinkSynthetic(t *testing.T) (*Store, *PluginStore, reviewedRemoveLinkSnapshot) {
	t.Helper()
	s, p := pluginStoreFixture(t)
	root := t.TempDir()
	for _, dir := range []string{"watch", "watch-long", "exclude", "target"} {
		if e := os.Mkdir(filepath.Join(root, dir), 0700); e != nil {
			t.Fatal(e)
		}
	}
	reviewedIYUUOwnedSettings(t, s, Settings{AllowedRoots: []string{root}})
	in := reviewedRemoveLinkSnapshot{Version: 1, SourceContainer: strings.Repeat("a", 64), PluginID: "RemoveLink", InstanceID: "RemoveLink", SourceSHA: reviewedRemoveLinkSourceSHA, InstalledCount: 1, Data: map[string]json.RawMessage{}, Paths: []reviewedRemoveLinkPath{{"/old", root}}}
	in.Config = string(reviewedIYUUJSON(map[string]any{"enabled": false, "monitor_dirs": "/old/watch", "exclude_dirs": "/old/exclude", "exclude_keywords": "/old/watch", "strm_path_mappings": "/old/watch:local:/old/target\n/old/watch-long:/old/target\n/old/watch:/old/exclude"}))
	return s, p, in
}
func TestReviewedRemoveLinkFieldMappingAndRejections(t *testing.T) {
	_, _, in := reviewedRemoveLinkSynthetic(t)
	_, mapped, _, e := validateReviewedRemoveLink(reviewedIYUUJSON(in))
	if e != nil {
		t.Fatal(e)
	}
	var cfg map[string]any
	json.Unmarshal(mapped, &cfg)
	if cfg["exclude_keywords"] != "/old/watch" {
		t.Fatal("keywords remapped")
	}
	lines := strings.Split(cfg["strm_path_mappings"].(string), "\n")
	if len(lines) != 3 || !strings.Contains(lines[1], "watch-long") || !strings.HasSuffix(lines[2], "/exclude") {
		t.Fatal("ordered duplicate mappings changed", lines)
	}
	for _, value := range []string{"", "null", `{"enabled":true}`, `{"enabled":"false"}`, `{"monitor_dirs":"/outside"}`, `{"monitor_dirs":"/old/../outside"}`, `{"monitor_dirs":"/oldish/watch"}`, `{"strm_path_mappings":"/old/watch:alist:/old/target"}`, `{"strm_path_mappings":"invalid"}`, `{"monitor_dirs":42}`} {
		changed := in
		changed.Config = value
		if ValidateReviewedRemoveLinkInput(reviewedIYUUJSON(changed)) == nil {
			t.Fatalf("accepted invalid input %s", value)
		}
	}
	for _, change := range []func(*reviewedRemoveLinkSnapshot){func(v *reviewedRemoveLinkSnapshot) { v.FileCount = 1 }, func(v *reviewedRemoveLinkSnapshot) { v.SymlinkCount = 1 }, func(v *reviewedRemoveLinkSnapshot) { v.Data["pending"] = json.RawMessage(`[]`) }, func(v *reviewedRemoveLinkSnapshot) { v.SourceSHA = strings.Repeat("f", 64) }, func(v *reviewedRemoveLinkSnapshot) {
		v.Paths = append(v.Paths, reviewedRemoveLinkPath{"/old/watch", "/else"})
	}} {
		v := in
		v.Data = map[string]json.RawMessage{}
		change(&v)
		if ValidateReviewedRemoveLinkInput(reviewedIYUUJSON(v)) == nil {
			t.Fatal("unsupported snapshot accepted")
		}
	}
	minimal := in
	minimal.Paths = []reviewedRemoveLinkPath{}
	for _, cfg := range []string{`{}`, `{"enabled":null,"monitor_dirs":null}`, `{"enabled":false,"monitor_dirs":""}`} {
		minimal.Config = cfg
		if ValidateReviewedRemoveLinkInput(reviewedIYUUJSON(minimal)) != nil {
			t.Fatal("original disabled default rejected")
		}
	}
}
func TestReviewedRemoveLinkAtomicProofAndTargetDrift(t *testing.T) {
	ctx := context.Background()
	t.Run("rollback", func(t *testing.T) {
		s, p, in := reviewedRemoveLinkSynthetic(t)
		raw := reviewedIYUUJSON(in)
		fp, _ := ReviewedRemoveLinkFingerprint(raw)
		run, e := p.BeginMigration(ctx, fp, "RemoveLink", in.InstanceID)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.DB.Exec(`CREATE TRIGGER reject_reviewed_remove_link_receipt BEFORE INSERT ON plugin_migration_receipts BEGIN SELECT RAISE(ABORT,'synthetic receipt failure'); END`); e != nil {
			t.Fatal(e)
		}
		if _, e = p.ImportReviewedRemoveLink(ctx, run.ID, raw); e == nil {
			t.Fatal("receipt failure ignored")
		}
		for _, table := range []string{"plugin_installations", "plugin_values", "plugin_migration_receipts"} {
			var n int
			if e = s.DB.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); e != nil || n != 0 {
				t.Fatal("partial transaction", table, n, e)
			}
		}
		var n int
		s.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind=?`, reviewedRemoveLinkMappingKind).Scan(&n)
		if n != 0 {
			t.Fatal("proof escaped rollback")
		}
	})
	for _, kind := range []string{"settings", "directory", "symlink", "proof", "proof-missing"} {
		t.Run(kind, func(t *testing.T) {
			s, p, in := reviewedRemoveLinkSynthetic(t)
			raw := reviewedIYUUJSON(in)
			fp, _ := ReviewedRemoveLinkFingerprint(raw)
			run, e := p.BeginMigration(ctx, fp, "RemoveLink", in.InstanceID)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = p.ImportReviewedRemoveLink(ctx, run.ID, raw); e != nil {
				t.Fatal(e)
			}
			cfg, e := p.Config(ctx, in.InstanceID)
			if e != nil {
				t.Fatal(e)
			}
			switch kind {
			case "settings":
				reviewedIYUUOwnedSettings(t, s, Settings{})
			case "directory":
				path := filepath.Join(in.Paths[0].Target, "watch")
				if e = os.Rename(path, path+"-retained"); e != nil {
					t.Fatal(e)
				}
				if e = os.Mkdir(path, 0700); e != nil {
					t.Fatal(e)
				}
			case "symlink":
				path := filepath.Join(in.Paths[0].Target, "watch")
				if e = os.Rename(path, path+"-retained"); e != nil {
					t.Fatal(e)
				}
				if e = os.Symlink(path+"-retained", path); e != nil {
					t.Fatal(e)
				}
			case "proof-missing":
				if _, e = s.DB.Exec(`UPDATE records SET kind='synthetic_retained_proof' WHERE kind=?`, reviewedRemoveLinkMappingKind); e != nil {
					t.Fatal(e)
				}
			case "proof":
				if _, e = s.DB.Exec(`UPDATE records SET payload=x'00' WHERE kind=?`, reviewedRemoveLinkMappingKind); e != nil {
					t.Fatal(e)
				}
			}
			if _, e = p.ImportReviewedRemoveLink(ctx, run.ID, raw); e == nil {
				t.Fatal("drift accepted")
			}
			after, e := p.Config(ctx, in.InstanceID)
			if e != nil || !bytes.Equal(after.Value, cfg.Value) {
				t.Fatal("failed repeat changed config", e)
			}
		})
	}
}
func TestReviewedRemoveLinkTargetRejectsUnownedMissingAndSymlink(t *testing.T) {
	ctx := context.Background()
	s, _, in := reviewedRemoveLinkSynthetic(t)
	for _, kind := range []string{"unowned", "missing", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			v := in
			v.Paths = append([]reviewedRemoveLinkPath(nil), in.Paths...)
			if kind == "unowned" {
				v.Paths[0].Target = t.TempDir()
			}
			if kind == "missing" {
				v.Paths[0].Target = filepath.Join(in.Paths[0].Target, "never-created")
			}
			if kind == "symlink" {
				link := filepath.Join(t.TempDir(), "link")
				if e := os.Symlink(in.Paths[0].Target, link); e != nil {
					t.Fatal(e)
				}
				v.Paths[0].Target = link
			}
			if ValidateReviewedRemoveLinkTarget(ctx, s.DB, s.key, reviewedIYUUJSON(v)) == nil {
				t.Fatal("unsafe target accepted")
			}
		})
	}
}

func TestReviewedRemoveLinkPreservesSourceJSONSemantics(t *testing.T) {
	_, _, in := reviewedRemoveLinkSynthetic(t)
	in.Paths = []reviewedRemoveLinkPath{}
	for _, source := range []string{`{}`, `{"enabled":null,"monitor_dirs":null,"exclude_dirs":null,"strm_path_mappings":null}`, `{"enabled":false,"monitor_dirs":"","exclude_dirs":"","strm_path_mappings":"","unknown":{"nullable":null,"items":[1,"keep"]}}`} {
		in.Config = source
		_, mapped, _, e := validateReviewedRemoveLink(reviewedIYUUJSON(in))
		if e != nil {
			t.Fatal(e)
		}
		var want, got map[string]any
		if json.Unmarshal([]byte(source), &want) != nil || json.Unmarshal(mapped, &got) != nil || !reflect.DeepEqual(want, got) {
			t.Fatal("source field presence/value changed", string(mapped))
		}
	}
}

func TestReviewedRemoveLinkReplayChecksCurrentGenerationAndRun(t *testing.T) {
	ctx := context.Background()
	for _, transition := range []string{"config-reload", "reset", "uninstall", "proof-run"} {
		t.Run(transition, func(t *testing.T) {
			s, p, in := reviewedRemoveLinkSynthetic(t)
			raw := reviewedIYUUJSON(in)
			receipt, e := ImportReviewedRemoveLinkOwned(ctx, s.DB, s.key, raw)
			if e != nil {
				t.Fatal(e)
			}
			cfg, e := p.Config(ctx, in.InstanceID)
			if e != nil {
				t.Fatal(e)
			}
			switch transition {
			case "config-reload":
				if _, e = p.SaveConfig(ctx, in.InstanceID, cfg.Revision, map[string]any{"enabled": false, "exclude_keywords": "edited synthetic value"}); e != nil {
					t.Fatal(e)
				}
				p, e = NewPluginStore(ctx, s)
				if e != nil {
					t.Fatal(e)
				}
			case "reset":
				if e = p.Reset(ctx, in.InstanceID); e != nil {
					t.Fatal(e)
				}
			case "uninstall":
				if e = p.Uninstall(ctx, in.InstanceID); e != nil {
					t.Fatal(e)
				}
			case "proof-run":
				var sealed []byte
				if e = s.DB.QueryRow(`SELECT payload FROM records WHERE kind=? AND id=?`, reviewedRemoveLinkMappingKind, in.InstanceID).Scan(&sealed); e != nil {
					t.Fatal(e)
				}
				plain, e := p.unseal(reviewedRemoveLinkMappingKind, in.InstanceID, "main", sealed)
				if e != nil {
					t.Fatal(e)
				}
				var proof reviewedRemoveLinkProof
				if e = json.Unmarshal(plain, &proof); e != nil {
					t.Fatal(e)
				}
				proof.Migration = "synthetic-different-run"
				sealed, e = p.seal(reviewedRemoveLinkMappingKind, in.InstanceID, "main", reviewedIYUUJSON(proof))
				if e != nil {
					t.Fatal(e)
				}
				if _, e = s.DB.Exec(`UPDATE records SET payload=? WHERE kind=? AND id=?`, sealed, reviewedRemoveLinkMappingKind, in.InstanceID); e != nil {
					t.Fatal(e)
				}
				tx, e := s.DB.BeginTx(ctx, nil)
				if e != nil {
					t.Fatal(e)
				}
				e = p.verifyReviewedRemoveLinkReceiptTx(ctx, tx, proof.Binding, receipt.RunID, receipt.Digest)
				tx.Rollback()
				if e == nil {
					t.Fatal("replacement proof run accepted")
				}
			}
			before, e := p.Installation(ctx, in.InstanceID)
			if e != nil {
				t.Fatal(e)
			}
			beforeConfig, beforeErr := p.Config(ctx, in.InstanceID)
			targetErr := ValidateReviewedRemoveLinkTarget(ctx, s.DB, s.key, raw)
			_, applyErr := p.ImportReviewedRemoveLink(ctx, receipt.RunID, raw)
			if transition == "config-reload" {
				if targetErr != nil || applyErr != nil || before.Generation != 1 {
					t.Fatal("normal edit/reload broke replay", targetErr, applyErr)
				}
			} else if targetErr == nil || applyErr == nil {
				t.Fatal("retired or changed proof accepted", targetErr, applyErr)
			}
			after, e := p.Installation(ctx, in.InstanceID)
			if e != nil || !reflect.DeepEqual(after, before) {
				t.Fatal("replay changed installation", e)
			}
			afterConfig, afterErr := p.Config(ctx, in.InstanceID)
			if (beforeErr == nil) != (afterErr == nil) || !bytes.Equal(beforeConfig.Value, afterConfig.Value) {
				t.Fatal("replay overwrote current config")
			}
		})
	}
}
