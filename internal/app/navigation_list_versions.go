package app

import (
	"fmt"
	"strings"
)

// List data and its revision are read in one transaction. All mutations,
// including imports and direct SQLite writers, invalidate the matching list.
func navigationListVersionSchema() string {
	var ddl strings.Builder
	ddl.WriteString("CREATE TABLE IF NOT EXISTS navigation_list_versions(name TEXT PRIMARY KEY,version INTEGER NOT NULL); INSERT OR IGNORE INTO navigation_list_versions VALUES('jobs',0),('history',0),('metadata',0);")
	ddl.WriteString("CREATE TABLE IF NOT EXISTS navigation_job_changes(slot INTEGER PRIMARY KEY,version INTEGER NOT NULL,source TEXT NOT NULL,old_id TEXT,new_id TEXT);")
	for _, table := range []string{"jobs", "events", "downloadhistory", "subscribe", "import_plan", "library_asset", "history_cleanup", "transferhistory"} {
		name := "jobs"
		if table == "transferhistory" {
			name = "history"
		}
		for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
			when := ""
			if table == "events" {
				switch event {
				case "INSERT":
					when = " WHEN NEW.job_id!=''"
				case "DELETE":
					when = " WHEN OLD.job_id!=''"
				default:
					when = " WHEN NEW.job_id!='' OR OLD.job_id!=''"
				}
			}
			body := "UPDATE navigation_list_versions SET version=version+1 WHERE name='" + name + "';"
			trigger := "nav_list_" + table + "_" + event
			if name == "jobs" {
				// Old triggers only bumped the overall revision; upgrade existing stores.
				fmt.Fprintf(&ddl, "DROP TRIGGER IF EXISTS %s;", trigger)
				trigger = "nav_change_" + table + "_" + event
				id := "id"
				switch table {
				case "events":
					id = "job_id"
				case "downloadhistory", "subscribe":
					id = "uid"
				}
				before, after := "OLD."+id, "NEW."+id
				if event == "INSERT" {
					before = "NULL"
				} else if event == "DELETE" {
					after = "NULL"
				}
				body += fmt.Sprintf("INSERT INTO navigation_job_changes SELECT version %% 128,version,'%s',%s,%s FROM navigation_list_versions WHERE name='jobs' ON CONFLICT(slot) DO UPDATE SET version=excluded.version,source=excluded.source,old_id=excluded.old_id,new_id=excluded.new_id;", table, before, after)
			}
			fmt.Fprintf(&ddl, "CREATE TRIGGER IF NOT EXISTS %s AFTER %s ON %s%s BEGIN %s END;", trigger, event, table, when, body)
		}
	}
	return ddl.String() + catalogPageStampSchema
}

type jobQueueDisplay struct {
	version  int64
	settings *Settings
	rows     []jobView
}

type historyPageDisplay struct {
	version int64
	total   int
	rows    []transferHistoryItem
}
