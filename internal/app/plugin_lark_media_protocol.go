package app

import (
	"math/big"
	"regexp"
	"strconv"
	"strings"

	lark "light-media/internal/plugins/larkmessager"
	"light-media/internal/releasemeta/legacyanime"
)

type larkMediaCallback struct {
	Request *string
	Action  string
	Index   *big.Int
}

func larkParseMediaCallback(value string) (*larkMediaCallback, error) {
	if strings.HasPrefix(value, "media:") {
		parts := strings.Split(value, ":")
		if len(parts) < 3 {
			return nil, nil
		}
		out := &larkMediaCallback{Request: &parts[1], Action: parts[2]}
		if len(parts) >= 4 && legacyanime.IsDigit(parts[3]) {
			n, e := legacyanime.Integer(parts[3])
			if e != nil {
				return nil, e
			}
			out.Index = n
		}
		return out, nil
	}
	if m := regexp.MustCompile(`^(select|download)_(` + legacyanime.DecimalClass() + `+)$`).FindStringSubmatch(strings.TrimSuffix(value, "\n")); m != nil {
		n, e := legacyanime.Integer(m[2])
		if e != nil {
			return nil, e
		}
		return &larkMediaCallback{Action: m[1], Index: n}, nil
	}
	if value == "page_p" {
		return &larkMediaCallback{Action: "page-prev"}, nil
	}
	if value == "page_n" {
		return &larkMediaCallback{Action: "page-next"}, nil
	}
	return nil, nil
}
func larkMediaPage(total, page int) (start, end, current, pages int) {
	pages = (total + 7) / 8
	if pages < 1 {
		pages = 1
	}
	if page < 0 {
		page = 0
	}
	if page >= pages {
		page = pages - 1
	}
	start = page * 8
	end = start + 8
	if end > total {
		end = total
	}
	return start, end, page, pages
}
func larkMediaNavigation(request string, page, pages int) [][]lark.Button {
	row := []lark.Button{}
	if page > 0 {
		row = append(row, lark.Button{Text: "⬅️ 上一页", CallbackData: "media:" + request + ":page-prev"})
	}
	if page < pages-1 {
		row = append(row, lark.Button{Text: "下一页 ➡️", CallbackData: "media:" + request + ":page-next"})
	}
	if len(row) == 0 {
		return [][]lark.Button{}
	}
	return [][]lark.Button{row}
}
func larkMediaButtons(request, phase string, count, total, page, pages int) [][]lark.Button {
	rows := [][]lark.Button{}
	if phase == "torrent" {
		rows = append(rows, []lark.Button{{Text: "🤖 自动选择下载", CallbackData: "media:" + request + ":download:0"}})
	}
	action := "select"
	if phase == "torrent" {
		action = "download"
	}
	if phase == "download-dir" {
		action = "download-dir"
	}
	row := []lark.Button{}
	for i := 1; i <= count; i++ {
		index := strconv.Itoa(i)
		row = append(row, lark.Button{Text: index, CallbackData: "media:" + request + ":" + action + ":" + index})
		if len(row) == 3 || i == count {
			rows = append(rows, row)
			row = []lark.Button{}
		}
	}
	if total > 8 {
		rows = append(rows, larkMediaNavigation(request, page, pages)...)
	}
	return rows
}
