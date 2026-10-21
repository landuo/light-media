package sites

import (
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"

	"light-media/internal/releasemeta/legacyanime"
)

func (r *sourceNexusReader) messages() {
	if r.err != nil || r.d.MessageUnread <= 0 {
		return
	}
	links := []string{}
	// The source uses an unordered set for these two boxes. Either whole-box
	// order is valid; links from both are collected before contents are read.
	for _, box := range []string{"1", "-2"} {
		ref := "messages.php?action=viewmailbox&box=" + box + "&unread=yes"
		for page := 0; ; page++ {
			if page >= 50 {
				r.err = errors.New("site_message_pagination_incomplete")
				return
			}
			doc := r.doc(r.page(r.resolved(r.origin(), ref), nil))
			nodes := r.x(doc, `//tr[not(./td/img[@alt="Read"])]/td/a[contains(@href,"viewmessage")]/@href`)
			for _, n := range nodes {
				links = append(links, sourceNexusText(n))
				if len(links) > 10000 {
					r.err = errors.New("site_message_contents_incomplete")
					return
				}
			}
			next := r.x(doc, `//a[contains(.//text(),"下一页") or contains(.//text(),"下一頁")]/@href`)
			if len(next) == 0 {
				break
			}
			ref = legacyanime.Strip(sourceNexusText(next[len(next)-1]))
			if ref == "" {
				break
			}
			if r.err != nil {
				return
			}
		}
	}
	if len(links) > 0 && r.d.MessageUnread == 0 {
		r.d.MessageUnread = int64(len(links))
		r.observed["message_unread"] = true
	}
	bytes := 0
	for _, link := range links {
		doc := r.doc(r.page(r.resolved(r.origin(), link), nil))
		if r.err != nil {
			return
		}
		var row [3]any
		if nodes := r.x(doc, `//h1/text()|//div[@class="layui-card-header"]/span[1]/text()`); len(nodes) > 0 {
			row[0] = legacyanime.Strip(sourceNexusText(nodes[len(nodes)-1]))
		}
		if nodes := r.x(doc, `//h1/following-sibling::table[.//tr/td[@class="colhead"]]//tr[2]/td[2]|//div[@class="layui-card-header"]/span[2]/span[2]`); len(nodes) > 0 {
			row[1] = legacyanime.Strip(sourceNexusText(nodes[0]))
		}
		if nodes := r.x(doc, `//h1/following-sibling::table[.//tr/td[@class="colhead"]]//tr[3]/td|//div[contains(@class,"layui-card-body")]`); len(nodes) > 0 {
			row[2] = legacyanime.Strip(sourceNexusText(nodes[0]))
		}
		raw, _ := json.Marshal(row)
		bytes += len(raw)
		if bytes > int(htmlLimit) {
			r.err = errors.New("site_message_contents_incomplete")
			return
		}
		r.d.Messages = append(r.d.Messages, row)
	}
	r.observed["messages"] = true
}

func (r *sourceNexusReader) seeding() {
	if r.err != nil || r.seed == "" {
		return
	}
	ref := r.seed
	for page := 0; page < 50; page++ {
		base := r.resolved(r.origin(), r.seed)
		u := r.resolved(base, ref)
		raw := r.page(u, r.seedParams)
		if r.err != nil {
			return
		}
		next := r.seedPage(strings.ReplaceAll(string(raw), `\/`, `/`), page > 0)
		if next == nil {
			r.seedComplete = len(raw) > 0
			return
		}
		ref = *next
	}
	r.err = errors.New("site_account_pagination_limit")
}

func (r *sourceNexusReader) seedPage(raw string, multi bool) *string {
	doc := r.doc([]byte(raw))
	if doc == nil || r.err != nil {
		return nil
	}
	if !multi {
		if nodes := r.x(doc, `//a[contains(@href,"torrents.php") and contains(@href,"seeding")]/@href`); len(nodes) > 0 {
			value := legacyanime.Strip(sourceNexusText(nodes[0]))
			if value != "" {
				r.seed = value
				return &value
			}
		}
	}
	sizeCol, seederCol := 3, 4
	sizesPath := `//tr[position()=1]/td[(img[@class="size"] and img[@alt="size"]) or (text()="大小") or (a/img[@class="size" and @alt="size"])]`
	seedersPath := `//tr[position()=1]/td[(img[@class="seeders"] and img[@alt="seeders"]) or (text()="在做种") or (a/img[@class="seeders" and @alt="seeders"])]`
	sizeHeader, seederHeader := len(r.x(doc, sizesPath)) > 0, len(r.x(doc, seedersPath)) > 0
	if sizeHeader {
		sizeCol = len(r.x(doc, sizesPath+`/preceding-sibling::td`)) + 1
	}
	if seederHeader {
		seederCol = len(r.x(doc, seedersPath+`/preceding-sibling::td`)) + 1
	}
	prefix := ""
	if len(r.x(doc, `//table[@class="torrents"]`)) > 0 {
		prefix = `//table[@class="torrents"]`
	}
	sizes := r.x(doc, prefix+`//tr[position()>1]/td[`+strconv.Itoa(sizeCol)+`]`)
	seeders := r.x(doc, prefix+`//tr[position()>1]/td[`+strconv.Itoa(seederCol)+`]/b/a/text()`)
	if len(seeders) == 0 {
		seeders = r.x(doc, prefix+`//tr[position()>1]/td[`+strconv.Itoa(seederCol)+`]//text()`)
	}
	var count, size int64
	info := [][2]int64{}
	allSizes, allPeers := true, true
	if len(sizes) > 0 && len(seeders) > 0 {
		count = int64(len(sizes))
		for i, node := range sizes {
			if i >= len(seeders) {
				r.err = sourceModuleError(nil)
				return nil
			}
			n, validSize := r.size(legacyanime.Strip(sourceNexusText(node)))
			peers, validPeers := sourceNexusStringInt(sourceNexusText(seeders[i]))
			allSizes = allSizes && validSize
			allPeers = allPeers && validPeers
			if !r.addSize(&size, n) {
				return nil
			}
			info = append(info, [2]int64{peers, n})
		}
	}
	r.d.Seeding += count
	if !r.addSize(&r.d.SeedingSize, size) {
		return nil
	}
	r.d.SeedingInfo = append(r.d.SeedingInfo, info...)
	if count > 0 || len(sizes) == 0 && sizeHeader && seederHeader {
		r.observed["seeding"], r.observed["seeding_bytes"] = true, allSizes
		r.seedShape = true
	}
	if !allSizes || !allPeers || len(sizes) > 0 && len(seeders) == 0 {
		r.seedInfoKnown = false
	}
	if !allSizes {
		r.seedSizesKnown = false
	}
	nodes := r.x(doc, `//a[contains(.//text(),"下一页") or contains(.//text(),"下一頁") or contains(.//text(),">")]/@href`)
	for len(nodes) > 0 {
		node := nodes[len(nodes)-1]
		nodes = nodes[:len(nodes)-1]
		next := legacyanime.Strip(sourceNexusText(node))
		if strings.HasPrefix(next, "details.php") {
			continue
		}
		if next == "" {
			return &next
		}
		value, ok, e := sourceNexusNextPage(next, r.id)
		if e != nil {
			r.err = e
		}
		if ok {
			return &value
		}
		return nil
	}
	return nil
}

func sourceNexusNextPage(ref string, id *string) (string, bool, error) {
	u, e := url.Parse(ref)
	if e != nil {
		return "", false, errors.New("site_source_link_invalid")
	}
	keys := []string{}
	values := map[string]string{}
	for _, piece := range strings.Split(u.RawQuery, "&") {
		if piece == "" {
			continue
		}
		pair := strings.SplitN(piece, "=", 2)
		key, e := url.QueryUnescape(pair[0])
		if e != nil {
			return "", false, errors.New("site_source_link_invalid")
		}
		value := ""
		if len(pair) > 1 {
			value, e = url.QueryUnescape(pair[1])
			if e != nil {
				return "", false, errors.New("site_source_link_invalid")
			}
		}
		if _, ok := values[key]; !ok {
			keys = append(keys, key)
		}
		values[key] = value
	}
	if values["userid"] != "" {
		return ref, true, nil
	}
	if id == nil || *id == "" {
		return "", false, nil
	}
	if _, ok := values["userid"]; !ok {
		keys = append(keys, "userid")
	}
	values["userid"] = *id
	if _, ok := values["type"]; !ok {
		keys = append(keys, "type")
		values["type"] = "seeding"
	}
	parts := []string{}
	for _, key := range keys {
		parts = append(parts, url.QueryEscape(key)+"="+url.QueryEscape(values[key]))
	}
	u.RawQuery = strings.Join(parts, "&")
	return u.String(), true, nil
}
