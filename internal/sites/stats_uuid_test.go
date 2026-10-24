package sites

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNexusUUIDAccountProfileAndSeedingStayBoundToOwnOrigin(t *testing.T) {
	const uuid = "11111111-2222-3333-4444-555555555555"
	for _, csrf := range []bool{false, true} {
		t.Run(fmt.Sprint("csrf-", csrf), func(t *testing.T) {
			var paths []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.URL.Path)
				if r.Header.Get("Cookie") != "synthetic-cookie" {
					t.Error("missing credential")
				}
				switch r.URL.Path {
				case "/index.php":
					fmt.Fprintf(w, `<a href="logout.php">退出</a><a href="https://evil.invalid/userdetails.php?uuid=%s">other</a><a href="userdetails.php?uuid=not-valid">invalid</a><a href="userdetails.php?uuid=%s">owned</a><div>上传：2 GB 下载：0 B</div>`, uuid, uuid)
				case "/userdetails.php":
					if r.URL.Query().Get("uuid") != uuid || r.URL.Query().Get("id") != "" {
						t.Error("profile selector changed")
					}
					fmt.Fprint(w, `<a href="logout.php">退出</a><table><tr><td>等级</td><td>Power User</td></tr><tr><td>加入日期</td><td>2024-01-02</td></tr></table>`)
					if csrf {
						fmt.Fprintf(w, `<meta name="x-csrf" content="synthetic-csrf"><a href="javascript: getusertorrentlistajax('%s','seeding')">做种</a>`, uuid)
					}
				case "/getusertorrentlistajax.php", "/ajax_getusertorrentlist.php":
					_ = r.ParseForm()
					if r.Form.Get("useruuid") != uuid || r.Form.Get("userid") != "" || r.Form.Get("type") != "seeding" {
						t.Error("seeding selector changed")
					}
					if csrf && (r.Method != "POST" || r.Form.Get("csrf") != "synthetic-csrf") {
						t.Error("lost CSRF")
					}
					fmt.Fprint(w, `<table><tr><th>名称</th><th>大小</th><th>做种者</th></tr><tr><td><a href="details.php?id=9">synthetic</a></td><td>1 GB</td><td>7</td></tr></table>`)
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
				}
			}))
			defer srv.Close()
			c, err := New(Config{ID: "owned", Kind: "jiayuan", BaseURL: srv.URL, Cookie: "synthetic-cookie"})
			if err != nil {
				t.Fatal(err)
			}
			s, err := c.UserStats(context.Background())
			if err != nil || s.UserID != uuid || s.Username != "owned" || s.Uploaded == nil || *s.Uploaded != 2*1024*1024*1024 || s.Downloaded == nil || *s.Downloaded != 0 || s.SeedingBytes == nil || *s.SeedingBytes != 1024*1024*1024 || len(s.SeedingInfo) != 1 || len(s.Warnings) != 0 {
				t.Fatal("UUID profile incomplete", s, err)
			}
			if len(paths) != 3 {
				t.Fatal("unexpected account requests", paths)
			}
		})
	}
}
