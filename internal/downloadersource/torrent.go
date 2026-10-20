// Package downloadersource projects the installed DownloaderTorrent attributes.
// It is separate from the public P0 download.Task and contains no credentials.
package downloadersource

import (
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"

	lark "light-media/internal/plugins/larkmessager"
)

var ErrDependency = errors.New("source_downloader_torrent_dependency")

// All26 fields of original schemas.transfer.DownloaderTorrent are explicit.
// Optional values preserve None. Paths are evaluated source Path.as_posix values;
// this JSON projection does not claim to recreate the full Python Path class.
type Torrent struct {
	Downloader       *string        `json:"downloader"`
	Hash             *string        `json:"hash"`
	Title            *string        `json:"title"`
	SiteName         *string        `json:"site_name"`
	Name             *string        `json:"name"`
	Year             *string        `json:"year"`
	SeasonEpisode    *string        `json:"season_episode"`
	Path             *string        `json:"path"`
	Size             *float64       `json:"size"`
	Progress         *float64       `json:"progress"`
	State            *string        `json:"state"`
	UpSpeed          *string        `json:"upspeed"`
	DLSpeed          *string        `json:"dlspeed"`
	Tags             *string        `json:"tags"`
	SavePath         *string        `json:"save_path"`
	ContentPath      *string        `json:"content_path"`
	Category         *string        `json:"category"`
	DownloadLimit    *float64       `json:"download_limit"`
	UploadLimit      *float64       `json:"upload_limit"`
	RatioLimit       *float64       `json:"ratio_limit"`
	SeedingTimeLimit *int64         `json:"seeding_time_limit"`
	Trackers         []string       `json:"trackers"`
	Media            map[string]any `json:"media"`
	UserID           *string        `json:"userid"`
	Username         *string        `json:"username"`
	LeftTime         *string        `json:"left_time"`
}
type Meta struct {
	Name          string
	Year          *string
	SeasonEpisode string
}
type NormalizePath func(string) (string, error)

func str(s string) *string   { return &s }
func num(n float64) *float64 { return &n }
func text(v any) (*string, error) {
	if v == nil {
		return nil, nil
	}
	if s, ok := v.(string); ok {
		return str(s), nil
	}
	return nil, ErrDependency
}
func number(v any) (*float64, error) {
	if v == nil {
		return nil, nil
	}
	var f float64
	switch n := v.(type) {
	case float64:
		f = n
	case int:
		f = float64(n)
	case int64:
		f = float64(n)
	case json.Number:
		var e error
		f, e = strconv.ParseFloat(string(n), 64)
		if e != nil {
			return nil, ErrDependency
		}
	default:
		return nil, ErrDependency
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return nil, ErrDependency
	}
	return num(f), nil
}
func fallbackNumber(v any) (float64, error) {
	n, e := number(v)
	if e != nil {
		return 0, e
	}
	if n == nil {
		return 0, nil
	}
	return *n, nil
}
func optional(r map[string]any, k string) (*string, error) { return text(r[k]) }
func StateQB(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "paused", "pauseddl", "pausedup", "stoppeddl", "stoppedup":
		return "paused"
	case "allocating", "checkingdl", "downloading", "forceddl", "metadl", "queueddl", "stalleddl":
		return "downloading"
	}
	return "completed"
}
func StateTR(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "stopped":
		return "paused"
	case "download_pending", "downloading":
		return "downloading"
	}
	return "completed"
}
func StringFloat(f float64) string { s, _ := lark.PythonString(f); return s }
func Rounded(f float64, n int) float64 {
	s := strconv.FormatFloat(f, 'f', n, 64)
	v, _ := strconv.ParseFloat(s, 64)
	return v
}
func FileSize(v any) (string, error) {
	if v == nil {
		return "", nil
	}
	s, e := lark.PythonString(v)
	if e != nil {
		return "", ErrDependency
	}
	s = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r >= 0x1c && r <= 0x1f {
			return -1
		}
		return r
	}, s) // source strips case-insensitive iB/B after whitespace
	s = strings.NewReplacer("iB", "", "ib", "", "IB", "", "Ib", "", "B", "", "b", "").Replace(s)
	numeric := s != ""
	for _, r := range s {
		if r != '.' && (r < '0' || r > '9') {
			numeric = false
			break
		}
	}
	if numeric {
		f, e := strconv.ParseFloat(s, 64)
		if e != nil {
			return "", nil
		}
		bounds := []float64{1023, 1048575, 1073741823, 1099511627775}
		i := sort.SearchFloat64s(bounds, f) - 1
		if i < 0 {
			return StringFloat(f) + "B", nil
		}
		return StringFloat(Rounded(f/(bounds[i]+1), 2)) + string("KMGT"[i]), nil
	}
	if strings.ContainsAny(strings.ToUpper(s), "KMGTP") {
		return s, nil
	}
	return s + "B", nil
}
func Seconds(f float64) string {
	hours := math.Floor(f / 3600)
	remainder := f - hours*3600
	minutes := math.Floor(remainder / 60)
	seconds := remainder - minutes*60
	out := strconv.FormatInt(int64(seconds), 10) + "秒"
	if minutes != 0 {
		out = strconv.FormatInt(int64(minutes), 10) + "分" + out
	}
	if hours != 0 {
		out = strconv.FormatInt(int64(hours), 10) + "时" + out
	}
	return out
}
func base(d string, m Meta) Torrent {
	return Torrent{Downloader: str(d), Name: str(m.Name), Year: m.Year, SeasonEpisode: str(m.SeasonEpisode), Trackers: []string{}, Media: map[string]any{}}
}
func BuildQB(r map[string]any, d string, m Meta, norm NormalizePath) (Torrent, error) {
	t := base(d, m)
	var e error
	if t.Title, e = optional(r, "name"); e != nil || t.Title == nil {
		return t, ErrDependency
	}
	if t.Hash, e = optional(r, "hash"); e != nil {
		return t, e
	}
	if t.Tags, e = optional(r, "tags"); e != nil {
		return t, e
	}
	if t.Category, e = optional(r, "category"); e != nil {
		return t, e
	}
	save, e := optional(r, "save_path")
	if e != nil {
		return t, e
	}
	content, e := optional(r, "content_path")
	if e != nil {
		return t, e
	}
	var p string
	if content != nil && *content != "" {
		p = *content
	} else {
		if save == nil {
			return t, ErrDependency
		}
		p = JoinPath(*save, *t.Title)
	}
	p, e = norm(p)
	if e != nil {
		return t, e
	}
	t.Path, t.ContentPath = str(p), str(p)
	if save != nil && *save != "" {
		s, e := norm(*save)
		if e != nil {
			return t, e
		}
		t.SavePath = str(s)
	}
	size, e := fallbackNumber(r["total_size"])
	if e != nil {
		return t, e
	}
	progress, e := fallbackNumber(r["progress"])
	if e != nil {
		return t, e
	}
	t.Size, t.Progress = num(size), num(progress*100)
	state, e := optional(r, "state")
	if e != nil {
		return t, e
	}
	s := ""
	if state != nil {
		s = *state
	}
	t.State = str(StateQB(s))
	dl, e := fallbackNumber(r["dlspeed"])
	if e != nil {
		return t, e
	}
	up, e := FileSize(r["upspeed"])
	if e != nil {
		return t, e
	}
	ds, e := FileSize(dl)
	if e != nil {
		return t, e
	}
	t.DLSpeed, t.UpSpeed = str(ds), str(up)
	lim, e := fallbackNumber(r["dl_limit"])
	if e != nil {
		return t, e
	}
	t.DownloadLimit = num(lim / 1024)
	lim, e = fallbackNumber(r["up_limit"])
	if e != nil {
		return t, e
	}
	t.UploadLimit = num(lim / 1024)
	if t.RatioLimit, e = number(r["ratio_limit"]); e != nil {
		return t, e
	}
	if n, e := number(r["seeding_time_limit"]); e != nil {
		return t, e
	} else if n != nil {
		if math.Trunc(*n) != *n || *n >= math.Exp2(63) || *n < -math.Exp2(63) {
			return t, ErrDependency
		}
		v := int64(*n)
		t.SeedingTimeLimit = &v
	}
	left := ""
	if dl > 0 {
		completed, e := fallbackNumber(r["completed"])
		if e != nil {
			return t, e
		}
		left = Seconds((size - completed) / dl)
	}
	t.LeftTime = str(left)
	return t, nil
}

// BuildTR consumes evaluated transmission-rpc attributes (not raw P0 Task).
func BuildTR(r map[string]any, d string, m Meta, norm NormalizePath) (Torrent, error) {
	t := base(d, m)
	var e error
	if t.Title, e = optional(r, "name"); e != nil || t.Title == nil {
		return t, ErrDependency
	}
	if t.Hash, e = optional(r, "hashString"); e != nil {
		return t, e
	}
	save, e := optional(r, "download_dir")
	if e != nil || save == nil {
		return t, ErrDependency
	}
	p, e := norm(JoinPath(*save, *t.Title))
	if e != nil {
		return t, e
	}
	t.Path, t.ContentPath = str(p), str(p)
	if *save != "" {
		s, e := norm(*save)
		if e != nil {
			return t, e
		}
		t.SavePath = str(s)
	}
	pick := func(names ...string) any {
		for _, k := range names {
			if v, ok := r[k]; ok {
				return v
			}
		}
		return nil
	}
	size, e := fallbackNumber(pick("total_size", "totalSize"))
	if e != nil {
		return t, e
	}
	progress, e := fallbackNumber(pick("progress", "percent_done"))
	if e != nil {
		return t, e
	}
	t.Size, t.Progress = num(size), num(progress)
	state, e := optional(r, "status")
	if e != nil {
		return t, e
	}
	s := ""
	if state != nil {
		s = *state
	}
	t.State = str(StateTR(s))
	dl, e := fallbackNumber(pick("rate_download", "rateDownload"))
	if e != nil {
		return t, e
	}
	up, e := fallbackNumber(pick("rate_upload", "rateUpload"))
	if e != nil {
		return t, e
	}
	ds, e := FileSize(dl)
	if e != nil {
		return t, e
	}
	us, e := FileSize(up)
	if e != nil {
		return t, e
	}
	t.DLSpeed, t.UpSpeed = str(ds), str(us)
	labels := []string{}
	if v := r["labels"]; v != nil {
		a, ok := v.([]any)
		if !ok {
			return t, ErrDependency
		}
		for _, v := range a {
			s, ok := v.(string)
			if !ok {
				return t, ErrDependency
			}
			labels = append(labels, s)
		}
	}
	t.Tags = str(strings.Join(labels, ","))
	if t.DownloadLimit, e = number(pick("download_limit", "downloadLimit")); e != nil {
		return t, e
	}
	if t.UploadLimit, e = number(pick("upload_limit", "uploadLimit")); e != nil {
		return t, e
	}
	if t.RatioLimit, e = number(pick("seed_ratio_limit", "seedRatioLimit")); e != nil {
		return t, e
	}
	if n, e := number(pick("seed_idle_limit", "seedIdleLimit")); e != nil {
		return t, e
	} else if n != nil {
		if math.Trunc(*n) != *n || *n >= math.Exp2(63) || *n < -math.Exp2(63) {
			return t, ErrDependency
		}
		v := int64(*n)
		t.SeedingTimeLimit = &v
	}
	left := ""
	if dl > 0 {
		n, e := fallbackNumber(pick("left_until_done", "leftUntilDone"))
		if e != nil {
			return t, e
		}
		left = Seconds(n / dl)
	}
	t.LeftTime = str(left)
	return t, nil
}
func DownloadingMessage(ts []Torrent) (string, string, error) {
	if len(ts) == 0 {
		return "没有正在下载的任务！", "", nil
	}
	lines := make([]string, len(ts))
	for i, t := range ts {
		if t.Size == nil || t.Progress == nil {
			return "", "", ErrDependency
		}
		title := "None"
		if t.Title != nil {
			title = *t.Title
		}
		size, e := FileSize(*t.Size)
		if e != nil {
			return "", "", e
		}
		lines[i] = strconv.Itoa(i+1) + ". " + title + " " + size + " " + StringFloat(Rounded(*t.Progress, 1)) + "%"
	}
	return "共 " + strconv.Itoa(len(ts)) + " 个任务正在下载：", strings.Join(lines, "\n"), nil
}
