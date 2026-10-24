package app

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

func instanceReadTables(t *testing.T, a *App) map[string][]string {
	t.Helper()
	rows, e := a.Store.DB.Query(`SELECT name FROM sqlite_master WHERE type='table' AND (name LIKE 'plugin_%' OR name='jobs') ORDER BY name`)
	if e != nil {
		t.Fatal(e)
	}
	names := []string{}
	for rows.Next() {
		var n string
		if e = rows.Scan(&n); e != nil {
			t.Fatal(e)
		}
		names = append(names, n)
	}
	if e = rows.Err(); e != nil {
		t.Fatal(e)
	}
	rows.Close()
	out := map[string][]string{}
	for _, n := range names {
		rs, e := a.Store.DB.Query(`SELECT * FROM "` + strings.ReplaceAll(n, `"`, `""`) + `"`)
		if e != nil {
			t.Fatal(e)
		}
		cols, e := rs.Columns()
		if e != nil {
			t.Fatal(e)
		}
		out[n] = []string{}
		for rs.Next() {
			v := make([]any, len(cols))
			ptr := make([]any, len(cols))
			for i := range v {
				ptr[i] = &v[i]
			}
			if e = rs.Scan(ptr...); e != nil {
				t.Fatal(e)
			}
			b, e := json.Marshal(v)
			if e != nil {
				t.Fatal(e)
			}
			out[n] = append(out[n], string(b))
		}
		if e = rs.Err(); e != nil {
			t.Fatal(e)
		}
		rs.Close()
		sort.Strings(out[n])
	}
	return out
}
