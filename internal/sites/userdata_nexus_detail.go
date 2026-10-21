package sites

import (
	"errors"
	"math"
	"net/url"
	"strings"

	"golang.org/x/net/html"
	"light-media/internal/releasemeta/legacyanime"
)

func (r *sourceNexusReader) detailInfo(doc *html.Node) {
	if doc == nil || r.err != nil {
		return
	}
	r.level(doc)
	if r.d.Bonus == 0 {
		if nodes := r.x(doc, `//tr/td[text()="魔力值" or text()="猫粮"]/following-sibling::td[1]/text()`); len(nodes) > 0 {
			n, valid := sourceNexusStringFloat(legacyanime.Strip(sourceNexusText(nodes[0])))
			r.d.Bonus = n
			r.observed["bonus"] = valid
		}
	}
	if nodes := r.x(doc, `//tr/td[text()="加入日期" or text()="注册日期" or *[text()="加入日期"]]/following-sibling::td[1]//text()|//div/b[text()="加入日期"]/../text()|//*[@id="outer"]/table/tr/td/div/div[1]/div[2]/div[3]/span[1]/span/@title`); len(nodes) > 0 {
		value := legacyanime.Strip(strings.SplitN(sourceNexusText(nodes[0]), " (", 2)[0])
		value = sourceNexusJoinedAt(value, r.now)
		r.d.JoinAt = &value
	}
	sizes := r.x(doc, `//tr/td[text()="当前上传"]/following-sibling::td[1]//table[tr[1][td[4 and text()="尺寸"]]]//tr[position()>1]/td[4]`)
	seeders := r.x(doc, `//tr/td[text()="当前上传"]/following-sibling::td[1]//table[tr[1][td[5 and text()="做种者"]]]//tr[position()>1]/td[5]//text()`)
	count := int64(len(sizes))
	var size int64
	info := [][2]int64{}
	for i, node := range sizes {
		if i >= len(seeders) {
			r.err = sourceModuleError(nil)
			return
		}
		n, validSize := r.size(legacyanime.Strip(sourceNexusText(node)))
		seeds, validPeers := sourceNexusStringInt(sourceNexusText(seeders[i]))
		if !validSize {
			r.seedSizesKnown = false
		}
		if !validSize || !validPeers {
			r.seedInfoKnown = false
		}
		if !r.addSize(&size, n) {
			return
		}
		info = append(info, [2]int64{seeds, n})
	}
	if r.d.SeedingSize == 0 {
		r.d.SeedingSize = size
		if len(sizes) > 0 {
			r.observed["seeding_bytes"] = r.seedSizesKnown
		}
	}
	if r.d.Seeding == 0 {
		r.d.Seeding = count
		if len(sizes) > 0 {
			r.observed["seeding"] = true
		}
	}
	if len(r.d.SeedingInfo) == 0 {
		r.d.SeedingInfo = info
	}
	if nodes := r.x(doc, `//tr/td[text()="做种统计"]/following-sibling::td[1]//text()`); len(nodes) > 0 {
		value := sourceNexusText(nodes[0])
		count, size = 0, 0
		if m := r.m(`总做种数:\s+(\d+)`, value, true); len(m) > 1 {
			count, _ = sourceNexusStringInt(m[1])
			r.observed["seeding"] = true
		}
		if m := r.m(`总做种体积:\s+([\d,.\s]+[KMGTPI]*B)`, value, true); len(m) > 1 {
			var valid bool
			size, valid = r.size(legacyanime.Strip(m[1]))
			r.observed["seeding_bytes"] = valid
		}
	}
	if r.d.SeedingSize == 0 {
		r.d.SeedingSize = size
	}
	if r.d.Seeding == 0 {
		r.d.Seeding = count
	}
	r.fixSeedingPage(doc)
	if r.c.config.Kind == "hhanclub" {
		if nodes := r.x(doc, `//span[contains(text(),"加入日期")]/following-sibling::span/span/@title`); len(nodes) > 0 {
			value := sourceNexusJoinedAt(legacyanime.Strip(sourceNexusText(nodes[0])), r.now)
			r.d.JoinAt = &value
		}
	}
}

func (r *sourceNexusReader) level(doc *html.Node) {
	for _, expr := range []string{`//tr/td[text()="等級" or text()="等级" or *[text()="等级"]]/following-sibling::td[1]/img[1]/@title`, `//tr/td[text()="等級" or text()="等级"]/following-sibling::td[1 and not(img)]|//tr/td[text()="等級" or text()="等级"]/following-sibling::td[1 and img[not(@title)]]`, `//tr/td[text()="等級" or text()="等级"]/following-sibling::td[1]`} {
		if nodes := r.x(doc, expr); len(nodes) > 0 {
			value := legacyanime.Strip(sourceNexusText(nodes[0]))
			r.d.UserLevel = &value
			goto hhan
		}
	}
	if r.d.UserLevel == nil || *r.d.UserLevel == "" {
		for _, node := range r.x(doc, `//a[contains(@href,"userdetails")]/text()`) {
			if m := r.m(`\[(.*)]`, sourceNexusText(node), false); len(m) > 1 && legacyanime.Strip(m[1]) != "" {
				value := legacyanime.Strip(m[1])
				r.d.UserLevel = &value
				break
			}
		}
	}
hhan:
	if r.c.config.Kind == "hhanclub" {
		if nodes := r.x(doc, `//b[contains(@class,"_Name")]/text()`); len(nodes) > 0 {
			value := sourceNexusText(nodes[0])
			r.d.UserLevel = &value
		}
	}
}

func (r *sourceNexusReader) fixSeedingPage(doc *html.Node) {
	if nodes := r.x(doc, `//a[contains(@href,"getusertorrentlist.php") and contains(@href,"seeding")]/@href`); len(nodes) > 0 {
		r.seed = legacyanime.Strip(sourceNexusText(nodes[0]))
	}
	links := r.x(doc, `//a[contains(@href,"javascript: getusertorrentlistajax") and contains(@href,"seeding")]/@href`)
	csrf := r.x(doc, `//meta[@name="x-csrf"]/@content`)
	if r.seed == "" && len(links) > 0 {
		if m := r.m(`javascript: getusertorrentlistajax\(\s*'(\d+)`, sourceNexusText(links[0]), false); len(m) > 1 && legacyanime.Strip(m[1]) != "" {
			id := legacyanime.Strip(m[1])
			r.id = &id
			r.d.UserID = id
			r.seed = "getusertorrentlistajax.php?userid=" + id + "&type=seeding"
		}
	} else if len(links) > 0 && len(csrf) > 0 {
		if token := legacyanime.Strip(sourceNexusText(csrf[0])); token != "" {
			r.seed = "ajax_getusertorrentlist.php"
			r.seedParams = url.Values{"type": {"seeding"}, "csrf": {token}}
			if r.id != nil {
				r.seedParams.Set("userid", *r.id)
			}
		}
	}
}

func (r *sourceNexusReader) addSize(total *int64, n int64) bool {
	if n > 0 && *total > math.MaxInt64-n || n < 0 && *total < math.MinInt64-n {
		r.err = errors.New("site_user_integer_limit")
		return false
	}
	*total += n
	return true
}

func (r *sourceNexusReader) native() {
	if r.id != nil {
		r.s.UserID = *r.id
		r.s.Sources["user_id"] = "root:source userdetails"
	}
	if r.d.Username != nil {
		r.s.Username = statsText(*r.d.Username, 128)
		r.s.Sources["username"] = "root:source username"
	}
	if r.d.UserLevel != nil {
		r.s.Level = statsText(*r.d.UserLevel, 80)
		r.s.Sources["level"] = "userdetails:source level"
	}
	if r.d.JoinAt != nil {
		r.s.JoinedAt = *r.d.JoinAt
		r.s.Sources["joined_at"] = "userdetails:source join date"
	}
	for _, f := range []struct {
		key   string
		value int64
		dst   **int64
	}{{"uploaded", r.d.Upload, &r.s.Uploaded}, {"downloaded", r.d.Download, &r.s.Downloaded}, {"seeding", r.d.Seeding, &r.s.Seeding}, {"seeding_bytes", r.d.SeedingSize, &r.s.SeedingBytes}, {"leeching", r.d.Leeching, &r.s.Leeching}, {"message_unread", r.d.MessageUnread, &r.s.MessageUnread}} {
		if r.observed[f.key] && f.value >= 0 && (f.key != "seeding_bytes" || r.seedSizesKnown) {
			v := f.value
			*f.dst = &v
			r.s.Sources[f.key] = "source NexusPHP field"
		}
	}
	for _, f := range []struct {
		key   string
		value float64
		dst   **float64
	}{{"ratio", r.d.Ratio, &r.s.Ratio}, {"bonus", r.d.Bonus, &r.s.Bonus}} {
		if r.observed[f.key] && f.value >= 0 {
			v := f.value
			*f.dst = &v
			r.s.Sources[f.key] = "source NexusPHP field"
		}
	}
	if r.seedComplete && r.seedShape && r.seedInfoKnown {
		valid := true
		for _, pair := range r.d.SeedingInfo {
			if pair[0] < 0 || pair[1] < 0 {
				valid = false
			}
		}
		if valid {
			r.s.SeedingInfo = append([][2]int64{}, r.d.SeedingInfo...)
			r.s.Sources["seeding_info"] = "source NexusPHP pages"
		}
	}
	if r.observed["messages"] {
		r.s.Messages = [][3]*string{}
		for _, row := range r.d.Messages {
			var values [3]*string
			for i, v := range row {
				if v != nil {
					s, ok := v.(string)
					if !ok {
						r.s.Messages = nil
						return
					}
					values[i] = &s
				}
			}
			r.s.Messages = append(r.s.Messages, values)
		}
		r.s.Sources["message_unread_contents"] = "source NexusPHP messages"
	}
}
