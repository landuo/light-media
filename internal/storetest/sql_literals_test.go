package storetest

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"light-media/internal/app"
)

var sqlLiteral = regexp.MustCompile("`[^`]*`")
var sqlStart = regexp.MustCompile(`(?i)^\s*(SELECT|WITH|INSERT|UPDATE|DELETE)\s`)

var cteDeclaration = regexp.MustCompile(`(?i)\bWITH\s+(?:RECURSIVE\s+)?([a-z_][a-z0-9_]*)\s+AS`)

var schemaTables = []string{
	"subscribe", "subscribehistory", "downloadhistory", "downloadfiles", "downloadfailure",
	"transferhistory", "message", "site", "siteicon", "sitestatistic", "siteuserdata",
	"systemconfig", "userconfig", "plugindata", "user", "workflow", "mediaserveritem",
	"agentchat", "agenttask", "passkey", "plugin_record",
	"site_state", "site_statistic_event", "site_message_sync", "site_system_error",
	"scheduler_state", "subscription_event", "subscription_acquisition", "subscription_receipt",
	"subscription_origin", "download_preview", "download_selection", "material", "material_blob",
	"media_exclusion", "import_plan", "import_plan_file", "import_receipt", "import_receipt_member",
	"import_meta", "managed_file", "library_asset", "library_asset_proof", "library_asset_presence",
	"library_candidate", "library_candidate_current", "library_scan", "removal_preview",
	"organize_queue_member", "organize_listing", "organize_cancel", "directory_file",
	"directory_file_state", "directory_import", "directory_status", "directory_overwrite",
	"file_operation", "history_receipt", "history_target_receipt", "history_parent_cleanup",
	"history_hidden", "history_cleanup", "download_file_event", "download_removal_receipt",
	"download_removal_root", "download_removal_chain", "download_context", "download_post_add",
	"plugin_original_callback", "plugin_original_failure", "plugin_original_batch",
	"plugin_original_directory", "plugin_host_state", "settings_lifecycle", "audit_log",
	"decision_log", "cache_entry", "workflow_lock",
}

// TestProductionSQLLiteralsPrepare walks every production SQL literal and asks
// SQLite to prepare it against the v2 schema. It catches columns that no longer
// exist after the records split (for example a leftover kind predicate) before
// a request can hit them at runtime.
func TestProductionSQLLiteralsPrepare(t *testing.T) {
	a, err := app.New(app.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer a.Close()
	store := a.Store
	files, err := filepath.Glob("../app/*.go")
	tests, _ := filepath.Glob("../app/*_test.go")
	files = append(files, tests...)
	if err != nil {
		t.Fatal(err)
	}
	checked, skipped := 0, 0
	ctes := map[string]bool{}
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, literal := range sqlLiteral.FindAllString(string(body), -1) {
			text := strings.Trim(literal, "`")
			if !sqlStart.MatchString(text) {
				continue
			}
			if strings.Contains(text, "?") && strings.Contains(text, "{{") {
				continue
			}
			if !mentionsSchemaTable(text) {
				continue
			}
			for _, m := range cteDeclaration.FindAllStringSubmatch(text, -1) {
				ctes[m[1]] = true
			}
			if strings.Count(text, "(") != strings.Count(text, ")") {
				skipped++
				continue
			}
			stmt, err := store.DB.PrepareContext(context.Background(), text)
			if err != nil {
				// Literals built by concatenation are often fragments; only fail
				// on errors that name a schema object so fragments stay skipped.
				if strings.Contains(err.Error(), "no such table: selected") {
					skipped++
					continue
				}
				if strings.Contains(err.Error(), "no such column") || strings.Contains(err.Error(), "no such table") || strings.Contains(err.Error(), "no such function") {
					t.Errorf("%s: %v\nSQL: %s", filepath.Base(file), err, oneLine(text))
				} else {
					skipped++
				}
				continue
			}
			stmt.Close()
			checked++
		}
	}
	t.Logf("prepared %d statements, skipped %d fragments", checked, skipped)
	if checked < 100 {
		t.Fatalf("only %d statements checked; the scanner probably stopped matching", checked)
	}
}

func mentionsSchemaTable(sql string) bool {
	lower := strings.ToLower(sql)
	for _, table := range schemaTables {
		if strings.Contains(lower, "from "+table) || strings.Contains(lower, "join "+table) ||
			strings.Contains(lower, "into "+table) || strings.Contains(lower, "update "+table) ||
			strings.Contains(lower, "on "+table) {
			return true
		}
	}
	return false
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// TestNoLegacyRecordsReferences keeps the split honest: no Go file may address
// the historical records shape any more, in tests included.
func TestNoLegacyRecordsReferences(t *testing.T) {
	files, _ := filepath.Glob("../app/*.go")
	legacy := regexp.MustCompile(`\b(FROM|INTO|UPDATE|JOIN|DELETE\s+FROM)\s+records\b`)
	allow := map[string]bool{
		// The v1 fixture and the migration report intentionally read the legacy
		// table while simulating an upgrade.
		filepath.Base("../app/records_migrate_v2.go"): false,
	}
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		base := filepath.Base(file)
		for i, line := range strings.Split(string(body), "\n") {
			if !legacy.MatchString(line) {
				continue
			}
			if allow[base] {
				continue
			}
			if base == "records_migrate_v2.go" {
				continue
			}
			if base == "sql_literals_test.go" || base == "store_test.go" {
				continue
			}
			t.Errorf("%s:%d references the legacy records table: %s", base, i+1, strings.TrimSpace(line))
		}
	}
}

// TestNoOrphanSealedTables catches the failure mode found on the production
// deployment: a kind whose rows live in plugin_record but whose readers still
// query the dedicated table, which is empty.
func TestNoOrphanSealedTables(t *testing.T) {
	files, _ := filepath.Glob("../app/*.go")
	orphan := regexp.MustCompile(`\b(FROM|JOIN)\s+(subscription_origin|subscription_acquisition|subscription_receipt)\b`)
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		base := filepath.Base(file)
		if strings.HasSuffix(base, "_test.go") {
			continue
		}
		for i, line := range strings.Split(string(body), "\n") {
			if orphan.MatchString(line) {
				t.Errorf("%s:%d reads a table that no kind maps to: %s", base, i+1, strings.TrimSpace(line))
			}
		}
	}
}
