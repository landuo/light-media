package app

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"light-media/internal/plugins/statistic"
)

func TestSourceAccountRowPreservesAllNullableSQLiteFields(t *testing.T) {
	raw := []byte(`{"id":1,"domain":"owned.invalid","name":"旧名称","username":null,"userid":"007","user_level":null,"join_at":null,"bonus":null,"upload":1125899906842624.5,"download":0.25,"ratio":null,"seeding":0.0,"leeching":null,"seeding_size":1234.75,"leeching_size":null,"seeding_info":"{\"x\":[0,1.25]}","message_unread":null,"message_unread_contents":"null","err_msg":null,"updated_day":"2026-10-21","updated_time":"12:34:56"}`)
	r, h, e := decodeSourceAccount(raw)
	if e != nil || !h.Good || r.Username != nil || r.MessageUnread != nil || *r.UserID != "007" || *r.Download != 0.25 || *r.SeedingSize != 1234.75 || *r.SeedingInfo != `{"x":[0,1.25]}` || *r.MessageUnreadContents != "null" {
		t.Fatal("SQLite values lost their original type or precision", e)
	}
	page := statistic.Page(statistic.Selected{Current: []statistic.Data{r.statistic("owned", "owned.invalid")}})
	var cells []statistic.Node
	var walk func(statistic.Node)
	walk = func(n statistic.Node) {
		if n.Component == "tbody" && len(n.Content) == 1 {
			cells = n.Content[0].Content
		}
		for _, child := range n.Content {
			walk(child)
		}
	}
	for _, n := range page {
		walk(n)
	}
	if len(cells) != 10 {
		t.Fatal("original account table missing")
	}
	for _, i := range []int{2, 3} {
		encoded, e := json.Marshal(cells[i].Text)
		if e != nil || string(encoded) != "null" {
			t.Fatal("unknown username/level changed from null to a reported empty value")
		}
	}
	_, p := pluginStoreFixture(t)
	sealed, e := p.seal(sourceAccountKind, "owned-source", "1", raw)
	if e != nil {
		t.Fatal(e)
	}
	actual, e := p.unseal(sourceAccountKind, "owned-source", "1", sealed)
	if e != nil || !bytes.Equal(actual, raw) || bytes.Contains(sealed, []byte("旧名称")) {
		t.Fatal("raw account archive was normalized or not encrypted", e)
	}
	for _, change := range [][]byte{bytes.Replace(raw, []byte(`"id":1`), []byte(`"id":1,"id":2`), 1), bytes.Replace(raw, []byte(`"12:34:56"`), []byte(`"24:34:56"`), 1), bytes.Replace(raw, []byte(`"domain":"owned.invalid"`), []byte(`"domain":null`), 1)} {
		if _, _, e := decodeSourceAccount(change); e == nil {
			t.Fatal("invalid source account accepted")
		}
	}
}

func TestSourceAccountEqualTimeNativeAndOriginalOrderIsTransitive(t *testing.T) {
	rows := []accountObservation{{Header: sourceAccountHeader{ID: 1, Time: "12:00:00"}}, {Header: sourceAccountHeader{ID: 0, Time: "12:00:00"}}, {Header: sourceAccountHeader{ID: 2, Time: "12:00:00"}}}
	rank := map[int64]int{1: 1, 2: 0}
	less := func(i, j int) bool { return accountObservationLess(rows[i], rows[j], rank) }
	equal := func(i, j int) bool { return !less(i, j) && !less(j, i) }
	for a := range rows {
		for b := range rows {
			for c := range rows {
				if less(a, b) && less(b, c) && !less(a, c) || equal(a, b) && equal(b, c) && !equal(a, c) {
					t.Fatal("mixed native/source ordering is not a strict weak order")
				}
			}
		}
	}
	if !less(2, 0) || !less(0, 1) {
		t.Fatal("equal-time native row changed the original source sequence")
	}
}

func TestSourceAccountLatestMatchesOriginalSQLiteWithErrorsAndDuplicates(t *testing.T) {
	s, _ := pluginStoreFixture(t)
	if _, e := s.DB.Exec(`CREATE TABLE owned_original_siteuserdata(id INTEGER PRIMARY KEY,domain TEXT,updated_day TEXT,updated_time TEXT,err_msg TEXT)`); e != nil {
		t.Fatal(e)
	}
	values := []struct{ domain, day, clock, err string }{
		{"a.invalid", "2026-10-19", "23:59:59", ""},
		{"a.invalid", "2026-10-20", "08:00:00", ""},
		{"a.invalid", "2026-10-20", "09:00:00", "error"},
		{"a.invalid", "2026-10-21", "10:00:00", "error"},
		{"b.invalid", "2026-06-01", "22:00:00", ""},
		{"c.invalid", "2026-10-21", "12:00:00", "error"},
	}
	for i, v := range values {
		id := int64(i + 1)
		if _, e := s.DB.Exec(`INSERT INTO owned_original_siteuserdata VALUES(?,?,?,?,?)`, id, v.domain, v.day, v.clock, v.err); e != nil {
			t.Fatal(e)
		}
		if _, e := s.DB.Exec(`INSERT INTO source_site_account_rows VALUES('owned',?,?,?,?,?,?)`, id, v.domain, v.day, v.clock, v.err == "", []byte("owned opaque payload")); e != nil {
			t.Fatal(e)
		}
	}
	tx, e := s.DB.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	rows, e := tx.Query(`SELECT s.id FROM owned_original_siteuserdata s JOIN (SELECT domain,MAX(updated_day) latest_day FROM owned_original_siteuserdata WHERE err_msg IS NULL OR err_msg='' GROUP BY domain) d ON s.domain=d.domain AND s.updated_day=d.latest_day ORDER BY s.updated_time DESC`)
	if e != nil {
		t.Fatal(e)
	}
	want := []int64{}
	for rows.Next() {
		var id int64
		if e = rows.Scan(&id); e != nil {
			t.Fatal(e)
		}
		want = append(want, id)
	}
	rows.Close()
	actual, e := sourceAccountLatestIDsTx(context.Background(), tx, "owned")
	if e != nil || !reflect.DeepEqual(actual, want) || !reflect.DeepEqual(actual, []int64{5, 3, 2}) {
		t.Fatal("latest-good-day join lost same-day errors, duplicates or time ordering", e)
	}
}

// Exercise the retained core archive reader directly without inventing a
// completed plugin migration, importing private input, or bypassing proof checks.
func TestSourceAccountArchiveRowAndIndexTamperFailClosed(t *testing.T) {
	for _, corruption := range []string{"none", "ciphertext", "index", "missing-row", "header-digest"} {
		t.Run(corruption, func(t *testing.T) {
			s, p := pluginStoreFixture(t)
			ctx := context.Background()
			raw := []byte(`{"id":1,"domain":"owned.invalid","name":"Owned","username":null,"userid":"007","user_level":null,"join_at":null,"bonus":null,"upload":0.25,"download":0.5,"ratio":null,"seeding":null,"leeching":null,"seeding_size":1234.75,"leeching_size":null,"seeding_info":"null","message_unread":null,"message_unread_contents":"null","err_msg":null,"updated_day":"2026-10-21","updated_time":"12:34:56"}`)
			want, header, err := decodeSourceAccount(raw)
			if err != nil {
				t.Fatal(err)
			}
			binding := sourceAccountBinding{SourceFingerprint: "owned-archive", Rows: []sourceAccountHeader{header}}
			sealed, err := p.seal(sourceAccountKind, binding.SourceFingerprint, "1", raw)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.DB.Exec(`INSERT INTO source_site_account_rows VALUES(?,?,?,?,?,?,?)`, binding.SourceFingerprint, header.ID, header.Domain, header.Day, header.Time, header.Good, sealed); err != nil {
				t.Fatal(err)
			}
			switch corruption {
			case "ciphertext":
				sealed[len(sealed)-1] ^= 1
				_, err = s.DB.Exec(`UPDATE source_site_account_rows SET sealed=?`, sealed)
			case "index":
				_, err = s.DB.Exec(`UPDATE source_site_account_rows SET updated_day='2099-01-01'`)
			case "missing-row":
				_, err = s.DB.Exec(`DELETE FROM source_site_account_rows`)
			case "header-digest":
				header.SHA = tokenHash("different owned row")
			}
			if err != nil {
				t.Fatal(err)
			}
			tx, err := s.DB.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			indexErr := sourceAccountHeadersTx(ctx, tx, binding)
			got, rowErr := p.sourceAccountRowTx(ctx, tx, binding, header)
			switch corruption {
			case "none":
				if indexErr != nil || rowErr != nil || !reflect.DeepEqual(got, want) {
					t.Fatal("authenticated core row lost nulls, fractions or JSON text", indexErr, rowErr)
				}
			case "index":
				if indexErr == nil {
					t.Fatal("tampered searchable index accepted")
				}
			case "missing-row":
				if indexErr == nil || rowErr == nil {
					t.Fatal("missing archive row accepted")
				}
			default:
				if rowErr == nil {
					t.Fatal("tampered encrypted row or expected digest accepted")
				}
			}
		})
	}
}
