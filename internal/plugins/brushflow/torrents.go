package brushflow

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"
)

func torrentHash(t DownloadState) string {
	if t.Kind == "qbittorrent" {
		return text(fallback(t.Raw["hash"], ""))
	}
	return text(fallback(t.Raw["hashString"], ""))
}
func torrentLabels(t DownloadState) []string {
	out := []string{}
	if t.Kind == "qbittorrent" {
		for _, v := range strings.Split(text(fallback(t.Raw["tags"], "")), ",") {
			if s := strings.TrimSpace(v); s != "" {
				out = append(out, s)
			}
		}
	} else {
		for _, v := range list(t.Raw["labels"]) {
			if s := strings.TrimSpace(text(v)); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}
func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
func (p *Core) torrentInfo(t DownloadState) Row {
	r := t.Raw
	now := int(p.h.Now().Unix())
	hash, title := torrentHash(t), text(r["name"])
	var added, done, active, total, progress, downloaded, uploaded, ratio float64
	var tags any
	tracker := ""
	if t.Kind == "qbittorrent" {
		added = number(r["added_on"])
		done = number(r["completion_on"])
		active = number(r["last_activity"])
		total = number(r["total_size"])
		progress = number(r["progress"])
		downloaded = number(r["downloaded"])
		uploaded = number(r["uploaded"])
		ratio = number(r["ratio"])
		tags = fallback(r["tags"], "")
		tracker = text(fallback(r["tracker"], ""))
	} else {
		added = number(fallback(r["added_date"], r["date_added"]))
		done = number(fallback(r["done_date"], r["date_done"]))
		active = number(fallback(r["activity_date"], r["date_active"]))
		total = number(r["total_size"])
		progress = number(r["progress"]) / 100
		downloaded = number(r["downloaded_ever"])
		if r["downloaded_ever"] == nil {
			downloaded = math.Trunc(total * progress)
		}
		ratio = number(r["ratio"])
		uploaded = number(r["uploaded_ever"])
		if r["uploaded_ever"] == nil {
			uploaded = math.Trunc(downloaded * ratio)
		}
		tags = fallback(r["labels"], "")
		ts := list(r["tracker_list"])
		if len(ts) > 0 {
			tracker = text(ts[0])
		}
	}
	dltime, seedtime, iatime := 0.0, 0.0, 0.0
	if added > 0 {
		dltime = float64(now) - added
	}
	if done > 0 {
		seedtime = float64(now) - done
	}
	if active > 0 {
		iatime = float64(now) - active
	}
	avg := uploaded
	if dltime != 0 {
		avg = math.Trunc(uploaded / dltime)
	}
	var actualProgress any = progress
	if t.Kind == "qbittorrent" {
		actualProgress = r["progress"]
	}
	return Row{"hash": hash, "title": title, "seeding_time": seedtime, "ratio": ratio, "uploaded": uploaded, "downloaded": downloaded, "progress": actualProgress, "avg_upspeed": avg, "iatime": iatime, "dltime": dltime, "total_size": total, "add_time": time.Unix(int64(added), 0).In(p.h.Now().Location()).Format("2006-01-02 15:04:05"), "add_on": added, "tags": tags, "tracker": tracker}
}
func (p *Core) siteByTorrent(ctx context.Context, t DownloadState) (int, string, error) {
	trackers := []string{}
	tracker := text(t.Raw["tracker"])
	if tracker == "" {
		ts := list(t.Raw["tracker_list"])
		if len(ts) > 0 {
			tracker = text(ts[0])
		}
	}
	if tracker != "" {
		trackers = append(trackers, tracker)
	}
	magnet := text(t.Raw["magnet_uri"])
	if t.Kind != "qbittorrent" {
		magnet = text(t.Raw["magnet_link"])
	}
	if magnet != "" {
		if u, e := url.Parse(magnet); e == nil {
			for _, v := range u.Query()["tr"] {
				s, e := url.PathUnescape(v)
				if e != nil {
					s = v
				}
				trackers = append(trackers, s)
			}
		}
	}
	sites, e := p.h.Sites(ctx)
	if e != nil {
		return 0, "", e
	}
	last := "未知"
	for _, tr := range trackers {
		if tr == "" {
			continue
		}
		domain := sourceDomain(tr)
		for _, m := range [][2]string{{"chdbits.xyz", "ptchdbits.co"}, {"agsvpt.trackers.work", "agsvpt.com"}, {"tracker.cinefiles.info", "audiences.me"}} {
			if strings.Contains(tr, m[0]) {
				domain = m[1]
				break
			}
		}
		if domain != "" {
			last = domain
		}
		for _, s := range sites {
			if sourceDomain(s.Domain) == domain {
				return s.ID, s.Name, nil
			}
		}
	}
	return 0, last, nil
}
func (p *Core) convertRecord(ctx context.Context, t Task, torrent DownloadState) (Row, error) {
	info := p.torrentInfo(torrent)
	site, name, e := p.siteByTorrent(ctx, torrent)
	if e != nil {
		return nil, e
	}
	r := Row{"task_id": t.ID(), "task_name": t.Name(), "site": site, "site_name": name, "title": fallback(info["title"], ""), "size": fallback(info["total_size"], 0), "pubdate": nil, "description": nil, "imdbid": nil, "page_url": nil, "date_elapsed": nil, "freedate": nil, "uploadvolumefactor": nil, "downloadvolumefactor": nil, "hit_and_run": false, "volume_factor": nil, "freedate_diff": nil, "ratio": info["ratio"], "downloaded": info["downloaded"], "progress": info["progress"], "uploaded": info["uploaded"], "seeding_time": info["seeding_time"], "deleted": false, "time": value(info, "add_on", float64(p.h.Now().UnixNano())/1e9)}
	return r, nil
}
func (p *Core) candidateRecord(t Task, s Site, v Row) Row {
	r := Row{"task_id": t.ID(), "task_name": t.Name(), "site": s.ID, "site_name": s.Name, "ratio": 0, "downloaded": 0, "uploaded": 0, "seeding_time": 0, "deleted": false, "time": float64(p.h.Now().UnixNano()) / 1e9, "add_on": float64(p.h.Now().UnixNano()) / 1e9}
	for _, k := range []string{"title", "size", "pubdate", "description", "imdbid", "page_url", "date_elapsed", "freedate", "uploadvolumefactor", "downloadvolumefactor", "volume_factor", "freedate_diff"} {
		r[k] = v[k]
	}
	r["hit_and_run"] = truth(v["hit_and_run"]) || t.B("site_hr_active")
	return r
}
func updateRecords(p *Core, ds []DownloadState, rs *RecordSet) {
	for _, d := range ds {
		r := rs.Values[torrentHash(d)]
		if r == nil {
			continue
		}
		info := p.torrentInfo(d)
		for _, k := range []string{"downloaded", "progress", "uploaded", "ratio", "seeding_time"} {
			r[k] = info[k]
		}
		r["add_on"] = fallback(info["add_on"], r["add_on"])
		r["pubdate"] = fallback(r["pubdate"], info["pubdate"])
	}
}
func filterTags(ds []DownloadState, s string) []DownloadState {
	if s == "" {
		return ds
	}
	excluded := []string{}
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			excluded = append(excluded, v)
		}
	}
	out := []DownloadState{}
	for _, d := range ds {
		blocked := false
		for _, l := range torrentLabels(d) {
			if contains(excluded, l) {
				blocked = true
			}
		}
		if !blocked {
			out = append(out, d)
		}
	}
	return out
}
func (p *Core) deleteNotice(ctx context.Context, t Task, r Row, reason string) error {
	if !t.B("notify") {
		return nil
	}
	return p.h.Message(ctx, Notice{Title: "【刷流任务种子删除】", Text: fmt.Sprintf("任务：%s\n站点：%s\n标题：%s\n原因：%s", t.Name(), text(fallback(r["site_name"], "未知")), text(fallback(r["title"], "未知")), reason)})
}
func filesize(v any) string {
	n := number(v)
	limits := []float64{1023, 1024*1024 - 1, 1024*1024*1024 - 1, 1024*1024*1024*1024 - 1}
	units := []string{"K", "M", "G", "T"}
	i := 0
	for i < len(limits) && limits[i] < n {
		i++
	}
	i--
	if i < 0 {
		return fmt.Sprintf("%.1fB", n)
	}
	if i >= len(limits) {
		i = len(limits) - 1
	}
	f := math.RoundToEven(n/(limits[i]+1)*100) / 100
	s := text(f)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s + units[i]
}
func buildAddText(r Row, name string) string {
	lines := []string{"任务：" + name}
	for _, pair := range [][2]string{{"site_name", "站点"}, {"title", "标题"}, {"description", "内容"}, {"size", "大小"}, {"pubdate", "发布时间"}, {"seeders", "做种数"}, {"volume_factor", "促销"}, {"hit_and_run", "Hit&Run"}} {
		v := r[pair[0]]
		if pair[0] == "size" && truth(v) {
			v = filesize(v)
		}
		if v == nil || v == "" || v == false || v == 0 || v == float64(0) {
			continue
		}
		s := text(v)
		if pair[0] == "hit_and_run" {
			s = "是"
		}
		lines = append(lines, pair[1]+"："+s)
	}
	return strings.Join(lines, "\n")
}
func (p *Core) addNotice(ctx context.Context, t Task, r Row) error {
	if !t.B("notify") {
		return nil
	}
	return p.h.Message(ctx, Notice{Title: "【刷流任务种子下载】", Text: buildAddText(r, t.Name())})
}
func (p *Core) syncNotice(ctx context.Context, t Task, title, status, reason string, rs []Row) error {
	if len(rs) == 0 {
		return nil
	}
	s := fmt.Sprintf("任务：%s\n内容：%s 等 %d 个种子已%s\n原因：%s", text(fallback(t.Name(), "未知")), text(fallback(rs[0]["title"], "未知种子")), len(rs), status, reason)
	p.h.Log(ctx, "info", title+"，"+s)
	if t.B("notify") {
		return p.h.Message(ctx, Notice{Title: title, Text: s})
	}
	return nil
}
