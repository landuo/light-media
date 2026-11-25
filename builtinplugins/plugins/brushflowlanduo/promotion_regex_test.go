package brushflowlanduo

import (
	pyre "light-media/builtinplugins/plugins/brushflowlanduo/compat/pythonre"
	"strings"
	"testing"
)

// Exercise the compiled page patterns through the same APIs as refreshPromotion;
// the pattern mix is fixed so the benchmark measures scans, not business branches.
func promotionRegexPage(promo bool) string {
	row := `<tr><td class="detail">影片详情 🎞 ` + strings.Repeat("合成文字 Sample. ", 12) + `</td></tr>` + "\n"
	body := strings.Repeat(row, 512) + `<a href="download.php?id=1">下载</a>`
	if promo {
		body += `<i class="pro_free">剩余时间：<span title="2026-12-01">Free until 2026-12-01</span></i>`
	}
	return body
}

func checkPageRegex(t testing.TB, body string, promo bool) {
	t.Helper()
	subject, err := pyre.DecodeSubject(body)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		key  string
		want bool
	}{
		{"TORRENT", true}, {"MARKER", promo}, {"NONE", false}, {"REMAINING_HINT", promo},
	} {
		if got, e := subject.Matches(sourcePatterns["PROMOTION_PAGE_"+tc.key+"_PATTERN"]); e != nil || got != tc.want {
			t.Fatalf("%s: got %v, %v; want %v", tc.key, got, e, tc.want)
		}
	}
	for _, key := range []string{"OTHER_VERSIONS", "REMAINING", "REMAINING_FALLBACK"} {
		m, e := subject.SearchGroups(sourcePatterns["PROMOTION_PAGE_"+key+"_PATTERN"], 0)
		want := promo && key != "OTHER_VERSIONS"
		if e != nil || (m != nil) != want {
			t.Fatalf("%s: match=%t, %v; want %v", key, m != nil, e, want)
		}
		if want && group(m, 1) != "2026-12-01" {
			t.Fatalf("%s: date = %q", key, group(m, 1))
		}
	}
}

func TestPromotionPageRegex(t *testing.T) {
	for _, promo := range []bool{true, false} {
		checkPageRegex(t, promotionRegexPage(promo), promo)
	}
}

func BenchmarkPromotionPageRegex(b *testing.B) {
	for _, tc := range []struct {
		name  string
		promo bool
	}{{"late-match", true}, {"no-promotion", false}} {
		b.Run(tc.name, func(b *testing.B) {
			body := promotionRegexPage(tc.promo)
			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			for b.Loop() {
				checkPageRegex(b, body, tc.promo)
			}
		})
	}
}
