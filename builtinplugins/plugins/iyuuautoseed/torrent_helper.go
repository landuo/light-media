package iyuuautoseed

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// TorrentHTTP is granted by the host for one selected site and URL lineage. The
// transport must validate every redirect before attaching site credentials. It
// does not expose a general network client to the plugin.
type TorrentHTTP interface {
	Get(context.Context, string) (*TorrentResponse, error)
	Post(context.Context, string, []TorrentFormField) (*TorrentResponse, error)
}
type TorrentFormField struct {
	Name, Value string
}
type TorrentResponse struct {
	Status  int
	Content []byte
	Text    string
	Headers http.Header
}
type TorrentCache interface {
	GetTorrent(context.Context, string) ([]byte, error)
	PutTorrent(context.Context, string, []byte) error
	AddInvalid(context.Context, string) error
}
type TorrentResult struct {
	CachePath    string
	Content      []byte
	Magnet       string
	Folder       string
	Files        []string
	ErrorMessage string
}
type TorrentHelper struct {
	HTTP  TorrentHTTP
	Cache TorrentCache
}

var firstForms = regexp.MustCompile(`(?s)<form.*?action="(.*?)".*?>(.*?)</form>`)
var firstInputs = regexp.MustCompile(`(?s)<input.*?name="(.*?)".*?value="(.*?)".*?>`)

func torrentCachePath(url string) string {
	h := md5.Sum([]byte(url))
	return hex.EncodeToString(h[:]) + ".torrent"
}
func (h TorrentHelper) Download(ctx context.Context, url string, cacheInvalid bool) (TorrentResult, error) {
	out := TorrentResult{Files: []string{}}
	if strings.HasPrefix(url, "magnet:") {
		out.Magnet = url
		out.ErrorMessage = "磁力链接"
		return out, nil
	}
	out.CachePath = torrentCachePath(url)
	if h.Cache == nil || h.HTTP == nil {
		return out, errors.New("torrent cache and site HTTP dependencies required")
	}
	cached, e := h.Cache.GetTorrent(ctx, out.CachePath)
	if e != nil {
		return out, e
	}
	if len(cached) > 0 {
		folder, files := TorrentFiles(cached)
		if folder != "" || len(files) > 0 {
			out.Content = bytes.Clone(cached)
			out.Folder = folder
			out.Files = files
			return out, nil
		}
	}
	req, e := h.HTTP.Get(ctx, url)
	if e != nil {
		return out, e
	}
	for req != nil && (req.Status == 301 || req.Status == 302) {
		location := req.Headers.Values("Location")
		if len(location) == 0 {
			return out, sourceFault("KeyError", "torrent response Location")
		}
		url = strings.Join(location, ", ")
		if strings.HasPrefix(url, "magnet:") {
			out.CachePath = ""
			out.Magnet = url
			out.ErrorMessage = "获取到磁力链接"
			return out, nil
		}
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		req, e = h.HTTP.Get(ctx, url)
		if e != nil {
			return out, e
		}
	}
	if req != nil && req.Status == 200 {
		if len(req.Content) == 0 {
			out.ErrorMessage = "未下载到种子数据"
			return out, nil
		}
		if bytes.HasPrefix(req.Content, []byte("magnet:")) {
			out.Magnet = req.Text
			out.ErrorMessage = "获取到磁力链接"
			return out, nil
		}
		if bytes.Contains(req.Content, []byte("下载种子文件")) {
			skipped := false
			for _, form := range firstForms.FindAllStringSubmatch(req.Text, -1) {
				if form[1] != "?" {
					continue
				}
				inputs := firstInputs.FindAllStringSubmatch(form[2], -1)
				if len(inputs) > 0 {
					data := make([]TorrentFormField, 0, len(inputs))
					positions := make(map[string]int, len(inputs))
					for _, in := range inputs {
						if i, ok := positions[in[1]]; ok {
							data[i].Value = in[2]
						} else {
							positions[in[1]] = len(data)
							data = append(data, TorrentFormField{Name: in[1], Value: in[2]})
						}
					}
					req, e = h.HTTP.Post(ctx, url, data)
					if e != nil {
						return out, e
					}
					if req != nil && req.Status == 200 {
						_, parseErr := torrentoolDecode(req.Content)
						skipped = parseErr == nil
					}
				}
				break
			}
			if !skipped {
				out.ErrorMessage = "种子数据有误，请确认链接是否正确，如为PT站点则需手工在站点下载一次种子"
				return out, nil
			}
		}
		if req != nil && len(req.Content) > 0 {
			// Original get_fileinfo catches parse errors internally: malformed content
			// is still returned with an empty file list, and is not put into the cache.
			out.Folder, out.Files = TorrentFiles(req.Content)
			if len(out.Files) > 0 {
				if e := h.Cache.PutTorrent(ctx, out.CachePath, req.Content); e != nil {
					if ctx.Err() != nil {
						return out, ctx.Err()
					}
					out.Folder, out.Files = "", []string{}
					out.ErrorMessage = "种子数据有误，请确认链接是否正确"
					return out, nil
				}
			}
			out.Content = bytes.Clone(req.Content)
		}
		return out, nil
	}
	if req == nil {
		out.ErrorMessage = "无法打开链接"
		return out, nil
	}
	if req.Status == 429 {
		out.ErrorMessage = "触发站点流控，请稍后重试"
		return out, nil
	}
	if cacheInvalid {
		if e := h.Cache.AddInvalid(ctx, url); e != nil {
			return out, e
		}
	}
	out.ErrorMessage = "下载种子出错，状态码：" + strconv.Itoa(req.Status)
	return out, nil
}

// TorrentFiles is the consumed installed torrentool + TorrentHelper projection.
// Invalid bencoding or missing fields is swallowed by the original helper.
func TorrentFiles(raw []byte) (folder string, files []string) {
	files = []string{}
	value, e := torrentoolDecode(raw)
	if e != nil {
		return "", files
	}
	root, ok := value.(map[string]any)
	if !ok {
		return "", files
	}
	info, ok := root["info"].(map[string]any)
	if !ok || len(info) == 0 {
		return "", files
	}
	name, ok := info["name"].(string)
	if !ok {
		return "", files
	}
	names := []string{}
	if list, present := info["files"]; present {
		entries, ok := list.([]any)
		if !ok {
			return "", files
		}
		for _, v := range entries {
			row, ok := v.(map[string]any)
			if !ok {
				return "", files
			}
			parts, e := sourceList(row["path"])
			if e != nil {
				return "", files
			}
			if _, ok := row["length"]; !ok {
				return "", files
			}
			joined := name
			for _, v := range parts {
				s, ok := v.(string)
				if !ok {
					return "", files
				}
				switch {
				case strings.HasPrefix(s, "/") || joined == "":
					joined = s
				case strings.HasSuffix(joined, "/"):
					joined += s
				default:
					joined += "/" + s
				}
			}
			names = append(names, joined)
		}
	} else {
		if _, ok := info["length"]; !ok {
			return "", files
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return "", files
	}
	if len(names) == 1 && names[0] == name {
		return "", []string{name}
	}
	folder = name
	for _, s := range names {
		// pathlib's consumed relative_to behavior normalizes repeated slashes and
		// dot segments but keeps `..`; path.Clean would erase those original parts.
		parts := pythonPathParts(s)
		if len(parts) == 0 {
			return "", []string{}
		}
		root := parts[0]
		if root == folder {
			parts = parts[1:]
			if len(parts) == 0 {
				files = append(files, ".")
			} else {
				files = append(files, strings.Join(parts, "/"))
			}
		} else {
			files = append(files, s)
		}
	}
	return folder, files
}
func pythonPathParts(s string) []string {
	out := []string{}
	if strings.HasPrefix(s, "//") && !strings.HasPrefix(s, "///") {
		out = append(out, "//")
	} else if strings.HasPrefix(s, "/") {
		out = append(out, "/")
	}
	for _, p := range strings.Split(s, "/") {
		if p != "" && p != "." {
			out = append(out, p)
		}
	}
	return out
}
