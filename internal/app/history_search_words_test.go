package app

import (
	"net/url"
	"path/filepath"
	"slices"
	"testing"
)

func TestHistorySearchPatternJiebaAndLiteralEscapes(t *testing.T) {
	for _, tc := range []struct{ query, pattern string }{
		{"", "%%"},
		{"流浪地球", "%流浪%地球%"},
		{"流浪 地球", "%流浪% %地球%"},
		{"A  B", "%A% % %B%"},
		{"100%_", `%100%\%%\_%`},
		{`x\y`, `%x%\\%y%`},
		{"流浪*地球?", "流浪%地球_"},
		{`*100%_?`, `%100\%\__`},
	} {
		if got := historySearchPattern(tc.query); got != tc.pattern {
			t.Errorf("query=%q got=%q want=%q", tc.query, got, tc.pattern)
		}
	}
}

func TestHistorySearchJiebaQueriesIndependentSQLFields(t *testing.T) {
	a, source, target := importTestApp(t)
	for _, tc := range []struct{ key, title, name string }{
		{"title", "前缀 流浪 地球 后缀", "one.mkv"},
		{"source", "SourceOnly", "流浪 中间 地球.mkv"},
		{"target", "TargetOnly", "three.mkv"},
		{"split", "流浪", "地球.mkv"},
		{"reverse", "地球流浪", "four.mkv"},
		{"anchored", "流浪 地球", "five.mkv"},
		{"literal", "100%_test", "six.mkv"},
		{"decoy", "100ABtest", "seven.mkv"},
	} {
		plan, _ := historyParityPlan(t, a, source, target, tc.key, tc.title, "failed", "error", tc.name)
		if tc.key == "target" {
			plan.Files[0].Target = filepath.Join(target, "流浪 中间 地球.mkv")
			if err := a.Store.Put(t.Context(), "import_plan", plan.ID, plan); err != nil {
				t.Fatal(err)
			}
			if _, err := a.Store.DB.Exec(`DELETE FROM transferhistory WHERE src=?`, plan.Files[0].Source); err != nil {
				t.Fatal(err)
			}
			tx, err := a.Store.DB.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if err = writeTransferHistoryMPTx(t.Context(), tx, transferHistoryMPInput{Plan: plan, File: plan.Files[0], Job: Job{ID: "search-target-job"}}); err != nil {
				tx.Rollback()
				t.Fatal(err)
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, tc := range []struct {
		query string
		keys  []string
	}{
		{"流浪地球", []string{"anchored", "source", "target", "title"}},
		{"流浪*地球", []string{"anchored"}},
		{"%_", []string{"literal"}},
		{"100%_test", []string{"literal"}},
	} {
		page := historyParityReadPage(t, a, "q="+url.QueryEscape(tc.query)+"&capabilities=deferred")
		got := []string{}
		for _, item := range page.Items {
			got = append(got, item.PlanID)
		}
		want := []string{}
		for _, key := range tc.keys {
			want = append(want, "parity-plan-"+key)
		}
		slices.Sort(got)
		slices.Sort(want)
		if page.Total != len(want) || !slices.Equal(got, want) {
			t.Fatalf("query=%q total=%d got=%v want=%v", tc.query, page.Total, got, want)
		}
	}
}
