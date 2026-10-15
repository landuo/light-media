package sites

import (
	"errors"
	"strings"

	"golang.org/x/net/html"
)

type nexusColumns struct {
	size, seeders, leechers, added int
}

func unrecognizedNexusRow(row *html.Node) bool {
	cells := directCells(row)
	for _, cell := range cells {
		if cell.Data == "th" || hasClass(cell, "colhead") || hasClass(cell, "colhead2") {
			return false
		}
	}
	if len(cells) == 1 {
		message := strings.ToLower(text(cells[0]))
		for _, marker := range []string{"没有找到", "沒有找到", "没有种子", "沒有種子", "no torrents found", "nothing found", "未找到"} {
			if strings.Contains(message, marker) {
				return false
			}
		}
	}
	return text(row) != ""
}

// Standard NexusPHP themes keep semantic headings (text or icon alt/class)
// even when optional columns move. Only complete, unambiguous headings override
// the classic table shape; unsupported templates fail explicitly.
func nexusSearchColumns(rows []*html.Node) (*nexusColumns, error) {
	for _, row := range rows {
		cells := directCells(row)
		cols := &nexusColumns{size: -1, seeders: -1, leechers: -1, added: -1}
		seen := map[string]int{}
		for i, cell := range cells {
			heading := cell.Data == "th" || hasClass(cell, "colhead") || hasClass(cell, "colhead2")
			if !heading {
				continue
			}
			labels := []string{text(cell)}
			for _, n := range find(cell, func(n *html.Node) bool { return n.Data == "img" || n.Data == "i" || n.Data == "span" }) {
				labels = append(labels, attribute(n, "alt"), attribute(n, "title"))
				labels = append(labels, strings.Fields(attribute(n, "class"))...)
			}
			for _, label := range labels {
				field := nexusHeading(label)
				if field == "" {
					continue
				}
				if old, found := seen[field]; found && old != i {
					return nil, errors.New("site_protocol_changed")
				}
				seen[field] = i
				switch field {
				case "size":
					cols.size = i
				case "seeders":
					cols.seeders = i
				case "leechers":
					cols.leechers = i
				case "added":
					cols.added = i
				}
			}
		}
		if cols.size >= 0 && cols.seeders >= 0 && cols.leechers >= 0 {
			if cols.size == cols.seeders || cols.size == cols.leechers || cols.seeders == cols.leechers {
				return nil, errors.New("site_protocol_changed")
			}
			return cols, nil
		}
		if len(seen) > 0 {
			return nil, errors.New("site_protocol_changed")
		}
	}
	return nil, nil
}

func nexusHeading(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "size", "大小", "尺寸":
		return "size"
	case "seeders", "seeding", "做种", "做種", "做种者", "做種者", "做种数", "做種數":
		return "seeders"
	case "leechers", "leeching", "下载", "下載", "下载者", "下載者", "下载数", "下載數":
		return "leechers"
	case "time", "added", "added at", "发布时间", "發佈時間", "上传时间", "上傳時間", "添加时间", "添加時間":
		return "added"
	}
	return ""
}

func nexusRowColumns(cells []*html.Node, columns *nexusColumns) (date, size, seeders, leechers *html.Node, err error) {
	if columns == nil {
		// Classic NexusPHP: category, title, comments, time, size, seeders,
		// leechers, completed, uploader. The outer size must validate; nested
		// title tables and a guessed shifted schema are never accepted.
		if len(cells) < 7 {
			return nil, nil, nil, nil, errors.New("site_protocol_changed")
		}
		columns = &nexusColumns{size: 4, seeders: 5, leechers: 6, added: 3}
	}
	for _, index := range []int{columns.size, columns.seeders, columns.leechers} {
		if index < 0 || index >= len(cells) {
			return nil, nil, nil, nil, errors.New("site_protocol_changed")
		}
	}
	if columns.added >= 0 && columns.added < len(cells) {
		date = cells[columns.added]
	}
	return date, cells[columns.size], cells[columns.seeders], cells[columns.leechers], nil
}
