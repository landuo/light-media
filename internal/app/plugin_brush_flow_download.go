package app

import (
	"bytes"
	"context"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/url"
	"strings"

	bf "light-media/internal/plugins/brushflow"
	native "light-media/internal/plugins/iyuuautoseed/native"
	sr "light-media/internal/plugins/seedrescue"
	"light-media/internal/sites"
)

func brushFlowSameJSON(a, b any) bool {
	left, le := cleanupJournalJSON(a)
	right, re := cleanupJournalJSON(b)
	return le == nil && re == nil && bytes.Equal(left, right)
}

// Decode opaque source rows without changing number lexemes before comparing
// them to the accepted candidate/effect bytes.
func brushFlowDecodeNumbers(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(out); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return brushFlowEvidenceError()
	}
	return nil
}
func (o *pluginBrushFlowOwner) metadata(ctx context.Context, task bf.Task, v bf.Row) ([]byte, string, error) {
	c := o.call(ctx)
	candidate, ok := c.candidates[brushFlowString(v["_brush_candidate"])]
	if !ok || !brushFlowSameJSON(candidate.Row, v) {
		return nil, "", brushFlowEvidenceError()
	}
	s, site, e := o.site(ctx, taskInt(task, "site_id"))
	if e != nil {
		return nil, "", e
	}
	if candidate.Site.ID != s.ID || tokenHash(string(mustCleanSeedJSON(candidate.Site))) != tokenHash(string(mustCleanSeedJSON(s))) {
		return nil, "", siteIdentityError()
	}
	if candidate.Resource != "" {
		var content []byte
		e = o.a.withSite(ctx, s, false, func(client *sites.Client) error {
			client.AddRequestGuard(o.nativeSiteGuard(s, site.ID))
			var e error
			content, e = client.Torrent(ctx, candidate.Resource)
			return e
		})
		return content, "", e
	}
	address := candidate.Enclosure
	if strings.HasPrefix(address, "magnet:") {
		return nil, address, nil
	}
	transport, e := o.siteTransport(ctx, s, site)
	if e != nil {
		return nil, "", e
	}
	defer transport.Close()
	cookie := true
	if strings.HasPrefix(address, "[]") {
		// Original __get_redict_url returns the suffix without an initial request.
		// Its caller clears cookies for every bracket recipe, including this one.
		address, cookie = address[2:], false
	} else if strings.HasPrefix(address, "[") {
		end := strings.LastIndex(address, "]")
		if end < 1 {
			return nil, "", nil
		}
		raw, e := base64.StdEncoding.DecodeString(address[1:end])
		if e != nil || len(raw) > 16384 {
			return nil, "", nil
		}
		var recipe map[string]any
		if json.Unmarshal(raw, &recipe) != nil {
			return nil, "", nil
		}
		address = address[end+1:]
		params, _ := recipe["params"].(map[string]any)
		parsed, e := url.Parse(address)
		if e != nil {
			return nil, "", e
		}
		query := parsed.Query()
		for k, v := range params {
			query.Set(k, brushFlowString(v))
		}
		parsed.RawQuery = query.Encode()
		headers := map[string]string{}
		if h, ok := recipe["header"].(map[string]any); ok {
			for k, v := range h {
				headers[k] = brushFlowString(v)
			}
		}
		if len(headers) == 0 {
			headers["User-Agent"] = seedRescueUA(s.UserAgent)
		}
		method := "POST"
		if recipe["method"] == "get" {
			method = "GET"
		}
		useCookie, _ := recipe["cookie"].(bool)
		response, e := transport.SeedRescueRequest(ctx, s.ID, method, parsed.String(), nil, headers, useCookie, true)
		if e != nil {
			return nil, "", e
		}
		if response.Status >= 400 {
			return nil, "", nil
		}
		var data any
		if path := brushFlowString(recipe["result"]); path == "" {
			data = response.Text
		} else {
			dec := json.NewDecoder(strings.NewReader(response.Text))
			dec.UseNumber()
			if dec.Decode(&data) != nil {
				return nil, "", nil
			}
			object, _ := data.(map[string]any)
			if success := brushFlowString(recipe["success"]); success != "" && !brushFlowTruthy(object[success]) {
				return nil, "", nil
			}
			for _, k := range strings.Split(path, ".") {
				object, ok := data.(map[string]any)
				if !ok {
					return nil, "", nil
				}
				data = object[k]
				if data == nil {
					return nil, "", nil
				}
			}
		}
		address = brushFlowString(data)
		if p, q := brushFlowString(recipe["result_path"]), brushFlowString(recipe["result_query_param"]); p != "" && q != "" {
			base, e := url.Parse(strings.TrimRight(brushFlowString(recipe["result_base_url"]), "/") + "/")
			if e != nil {
				return nil, "", e
			}
			relative, e := url.Parse(strings.TrimLeft(p, "/"))
			if e != nil {
				return nil, "", e
			}
			u := base.ResolveReference(relative)
			u.RawQuery = url.Values{q: []string{address}}.Encode()
			address = u.String()
		}
		cookie = false
	}
	if address == "" {
		return nil, "", nil
	}
	if strings.HasPrefix(address, "magnet:") {
		return nil, address, nil
	}
	if task.B("site_skip_tips") && s.Name != "天空" && site.Schema == "nexusphp" {
		u, e := url.Parse(address)
		if e == nil {
			q := u.Query()
			q.Set("letdown", "1")
			u.RawQuery = q.Encode()
			address = u.String()
		}
	}
	response, e := transport.SeedRescueRequest(ctx, s.ID, "GET", address, nil, map[string]string{"User-Agent": seedRescueUA(s.UserAgent)}, cookie, true)
	if e != nil {
		return nil, "", e
	}
	if response.Status >= 400 {
		return nil, "", nil
	}
	return response.Content, "", nil
}
func taskInt(t bf.Task, k string) int { return int(t.N(k)) }
func brushFlowTruthy(v any) bool {
	if v == nil {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x != ""
	case float64:
		return x != 0
	case json.Number:
		return x != "0"
	default:
		return true
	}
}
func (o *pluginBrushFlowOwner) Download(ctx context.Context, task bf.Task, v, record bf.Row, tag string) (hash string, err error) {
	c := o.call(ctx)
	if c == nil || task.ID() != c.taskID || c.operation != "brush" || len(tag) != 10 || record["task_id"] != task.ID() {
		return "", brushFlowEvidenceError()
	}
	content, magnet, e := o.metadata(ctx, task, v)
	if e != nil {
		return "", e
	}
	if len(content) == 0 && magnet == "" {
		return "", nil
	}
	expected := ""
	if len(content) > 0 {
		expected, e = sr.InfoHashFromTorrent(content)
	} else {
		u, e2 := url.Parse(magnet)
		if e2 != nil {
			return "", e2
		}
		for _, xt := range u.Query()["xt"] {
			const prefix = "urn:btih:"
			if len(xt) <= len(prefix) || !strings.EqualFold(xt[:len(prefix)], prefix) {
				continue
			}
			token := xt[len(prefix):]
			var raw []byte
			if len(token) == 40 {
				raw, e = hex.DecodeString(token)
			} else if len(token) == 32 {
				raw, e = base32.StdEncoding.DecodeString(strings.ToUpper(token))
			} else {
				continue
			}
			if e == nil && len(raw) == 20 {
				expected = hex.EncodeToString(raw)
				break
			}
		}
		if expected == "" {
			return "", bad("BRUSH_FLOW_TORRENT_INVALID", "磁力链接没有明确的种子身份")
		}

	}
	if e != nil {
		return "", e
	}
	expected = strings.ToLower(expected)
	client, d, e := o.downloader(ctx, task.S("downloader"))
	if e != nil {
		return "", e
	}
	defer client.Close()
	kind := seedRescueNativeConfig(d).Kind
	category := task.S("qb_category")
	tags := []string{"已整理", bf.GlobalTag, task.Tag()}
	if kind == "qbittorrent" {
		tags = append(tags, tag)
		if c.temporaryTags == nil {
			c.temporaryTags = map[string]string{}
		}
		c.temporaryTags[tag] = d.Name
	}
	// The original finally deletes the temporary global tag definition. Once an
	// earlier write is unknown, ioGuard forbids the cleanup from dispatching.
	if kind == "qbittorrent" {
		defer func() {
			_, cleanup := o.DeleteTags(ctx, d.Name, []string{tag})
			if cleanup != nil && err == nil {
				err = cleanup
			}
		}()
	}
	var save *string
	if raw := task.Row["save_path"]; raw != nil {
		s := brushFlowString(raw)
		save = &s
	}
	spec := native.IYUUAddSpec{Torrent: content, Magnet: magnet, SavePath: save, Category: &category, Tags: tags, IgnoreCategoryCheck: true}
	var destination *native.IYUUDestination
	if client.DestinationRequired(spec) {
		v, err := client.EffectiveAddDestination(ctx, spec)
		if err != nil {
			return "", err
		}
		destination = &v
	}
	var up, down, upBytes, downBytes *int64
	if n := task.N("up_speed"); n != 0 {
		v := int64(n)
		up = &v
		b := v * 1024
		upBytes = &b
	}
	if n := task.N("dl_speed"); n != 0 {
		v := int64(n)
		down = &v
		b := v * 1024
		downBytes = &b
	}
	args := bf.Row{"hash": expected, "record": record, "task_id": task.ID(), "downloader_id": d.ID, "downloader_name": d.Name, "downloader_sha": seedRescueDownloaderSHA(d), "torrent_sha": tokenHash(string(content)), "magnet_sha": tokenHash(magnet), "save_path": save, "category": category, "tags": tags, "destination": destination, "upload_bytes": upBytes, "download_bytes": downBytes}
	ordinal, e := c.beginEffect(ctx, "add_torrent", args)
	if e != nil {
		return "", e
	}
	result, callErr := client.BrushFlowAddDownloading(ctx, spec, upBytes, downBytes, destination)
	if callErr != nil && !native.MutationNotDispatched(callErr) {
		return "", c.unknown(callErr)
	}
	if hook := o.a.pluginBrushFlow.afterEffect; hook != nil {
		if e = hook("add_torrent"); e != nil {
			return "", c.unknown(e)
		}
	}
	if e = c.finishEffect(ctx, ordinal, "add_torrent", bf.Row{"accepted": result.Accepted, "source_truthy": result.SourceTruthy, "not_dispatched": callErr != nil, "failure_class": native.MutationFailureClass(callErr), "hash": result.Hash}); e != nil {
		return "", e
	}
	if callErr != nil {
		if native.MutationFailureClass(callErr) == "owner_denied" || native.MutationFailureClass(callErr) == "cancelled" {
			return "", &brushFlowBoundaryError{callErr}
		}
		if !result.SourceTruthy {
			return "", nil
		}
	}
	if !result.SourceTruthy {
		return "", nil
	}
	hash = result.Hash
	if kind == "qbittorrent" {
		hash, e = client.PollByTag(ctx, tag)
		if e != nil {
			return "", e
		}
	}
	if hash == "" {
		return "", nil
	}
	if !strings.EqualFold(hash, expected) {
		return "", c.unknown(bad("BRUSH_FLOW_HASH_MISMATCH", "下载器返回的种子身份不一致"))
	}
	hash = strings.ToLower(hash)
	// Confirmed add is recoverable even if the source later exits before its final
	// in-memory task-data flush. Preserve insertion order and exact source record.
	e = c.confirmAddedRecord(ctx, task.ID(), hash, record)
	if e != nil {
		return "", c.unknown(e)
	}
	if kind == "qbittorrent" {
		if e = o.removeTemporaryTag(ctx, client, d, hash, tag); e != nil {
			return "", e
		}
		if _, e = o.DeleteTags(ctx, d.Name, []string{tag}); e != nil {
			return "", e
		}
	}
	rows, e := o.torrents(ctx, d.Name, []string{hash}, false)
	if e != nil {
		return "", e
	}
	if len(rows) == 0 {
		return "", c.unknown(bad("BRUSH_FLOW_ADD_NOT_OBSERVED", "已接受的种子尚未在下载器中核对"))
	}
	if kind == "transmission" && (up != nil || down != nil) {
		_, e = o.control(ctx, d.Name, "limits", []string{hash}, nil, up, down)
		if e != nil {
			return "", e
		}
	}
	return hash, nil
}
func (c *brushFlowCall) confirmAddedRecord(ctx context.Context, task, hash string, record bf.Row) error {
	tx, e := c.o.a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = c.guardTx(ctx, tx); e != nil {
		return e
	}
	s, b, e := c.o.a.pluginStore.brushFlowRead(ctx, tx, c.o.instance, c.o.generation)
	if e != nil {
		return e
	}
	key := "task." + task + ".torrents"
	records := bf.NewRecords()
	if _, ok := s.Values[key]; ok {
		var sealed []byte
		if e = tx.QueryRowContext(ctx, `SELECT sealed FROM plugin_values WHERE instance_id=? AND kind='data' AND item_key=?`, c.o.instance, key).Scan(&sealed); e != nil {
			return e
		}
		raw, e := c.o.a.pluginStore.unseal("data", c.o.instance, key, sealed)
		if e != nil || records.UnmarshalJSON(raw) != nil {
			return brushFlowEvidenceError()
		}
	}
	records.Set(hash, record)
	raw, e := json.Marshal(records)
	if e != nil {
		return e
	}
	if e = c.writeValueTx(ctx, tx, &s, key, raw); e != nil {
		return e
	}
	if e = c.o.a.pluginStore.brushFlowSave(ctx, tx, s, b); e != nil {
		return e
	}
	return tx.Commit()
}
