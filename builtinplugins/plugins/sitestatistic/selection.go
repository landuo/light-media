package sitestatistic

import (
	"context"
	"sort"
)

// selectionCursor consists only of source __get_data loop locals. Every
// missing date is fetched once; inactive and unrequested rows are not validated.
type selectionCursor struct {
	selected    Selected
	cache       map[string]map[string]Data
	index, back int
	previous    Data
	wanted      string
	finished    bool
}

func beginSelection(ctx context.Context, h Host) (*selectionCursor, error) {
	latest, e := h.Latest(ctx)
	if e != nil {
		return nil, e
	}
	c := &selectionCursor{selected: Selected{Day: "", Current: []Data{}, Previous: []Data{}}, cache: map[string]map[string]Data{}, back: 1}
	if len(latest) == 0 {
		c.finished = true
		return c, nil
	}
	active, e := h.ActiveDomains(ctx)
	if e != nil {
		return nil, e
	}
	domains := map[string]bool{}
	for _, s := range active {
		domains[s] = true
	}
	for _, row := range latest {
		if row == nil {
			continue
		}
		s, ok := row["domain"].(string)
		if ok && domains[s] {
			c.selected.Current = append(c.selected.Current, row)
		}
	}
	if len(c.selected.Current) == 0 {
		c.finished = true
		return c, nil
	}
	c.selected.Day = c.selected.Current[0]["updated_day"]
	for _, d := range c.selected.Current {
		if pyText(d["updated_day"]) != pyText(c.selected.Day) {
			c.selected.Day = "各站点最近更新日"
			break
		}
	}
	sort.SliceStable(c.selected.Current, func(i, j int) bool {
		return number(c.selected.Current[i]["upload"]) > number(c.selected.Current[j]["upload"])
	})
	return c, nil
}
func (c *selectionCursor) advance(ctx context.Context, h Host) (err error) {
	defer recoverError(&err)
	if c.finished {
		return nil
	}
	for c.index < len(c.selected.Current) {
		current := c.selected.Current[c.index]
		day, e := sourceDate(current["updated_day"])
		if e != nil {
			return e
		}
		previousDay := day.AddDate(0, 0, -c.back)
		if previousDay.Year() < 1 {
			return &SourceError{"OverflowError", "date value out of range"}
		}
		date := sourceDay(previousDay)
		byName, known := c.cache[date]
		if !known {
			rows, e := h.ByDate(ctx, date)
			if e != nil {
				return e
			}
			byName = map[string]Data{}
			for _, d := range rows {
				byName[dataKey(d)] = d
			}
			c.cache[date] = byName
		}
		candidate := byName[dataKey(current)]
		if c.back == 1 {
			c.previous = candidate
		}
		if candidate != nil && !truth(candidate["err_msg"]) {
			c.previous = candidate
			c.finishCurrent()
		} else if c.back == 7 {
			c.finishCurrent()
		} else {
			c.back++
		}
		// One actual date RPC per SDK phase; already cached dates remain local.
		if !known {
			return nil
		}
	}
	c.finished = true
	return nil
}
func (c *selectionCursor) finishCurrent() {
	if c.previous != nil {
		c.selected.Previous = append(c.selected.Previous, c.previous)
	}
	c.previous = nil
	c.index++
	c.back = 1
}
func GetData(ctx context.Context, h Host) (selected Selected, err error) {
	defer recoverError(&err)
	c, e := beginSelection(ctx, h)
	if e != nil {
		return selected, e
	}
	for !c.finished {
		if e = c.advance(ctx, h); e != nil {
			return selected, e
		}
	}
	return c.selected, nil
}

type snapshotHost struct {
	Host
	snapshot Snapshot
}

func (h snapshotHost) Latest(context.Context) ([]Data, error) {
	return append([]Data{}, h.snapshot.Latest...), nil
}
func (h snapshotHost) ActiveDomains(context.Context) ([]string, error) {
	return h.snapshot.ActiveDomains, nil
}
func (h snapshotHost) ByDate(_ context.Context, date string) ([]Data, error) {
	rows := []Data{}
	for _, d := range h.snapshot.History {
		if d["updated_day"] == date {
			rows = append(rows, d)
		}
	}
	return rows, nil
}
func Select(s Snapshot) (Selected, error) {
	return GetData(context.Background(), snapshotHost{snapshot: s})
}
