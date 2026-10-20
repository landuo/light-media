package acquisitionbatch

import (
	"context"
	"encoding/json"
	"time"
)

type EpisodeFile struct {
	ID   json.Number
	Name string
}
type FileSelectionDependencies struct {
	Files    func(context.Context, int, time.Duration) ([]EpisodeFile, error)
	Episodes func(string) ([]int, error)
	SetFiles func(context.Context, []json.Number, bool) (bool, error)
	Start    func(context.Context, bool) (bool, error)
}
type FileSelectionResult struct {
	SelectedEpisodes []int
	MetadataMissing  bool
	Started          bool
}

// SelectFiles reproduces the downloader modules' post-Add branch. A failed
// setter's original False does not suppress Start; a native unknown error is
// separate and is returned to its receipt owner. The original hash remains an
// acknowledged Add even if its subsequent metadata lookup is empty/paused.
func SelectFiles(ctx context.Context, kind string, episodes []int, force bool, d FileSelectionDependencies) (FileSelectionResult, error) {
	out := FileSelectionResult{SelectedEpisodes: []int{}}
	if len(episodes) == 0 {
		return out, nil
	}
	attempts, interval := 1, time.Duration(0)
	if kind == "qbittorrent" {
		attempts, interval = 5, time.Second
	}
	files, e := d.Files(ctx, attempts, interval)
	if e != nil {
		return out, e
	}
	if len(files) == 0 {
		out.MetadataMissing = true
		return out, nil
	}
	wanted, unwanted := []json.Number{}, []json.Number{}
	for _, file := range files {
		eps, e := d.Episodes(file.Name)
		if e != nil {
			return out, e
		}
		if len(eps) == 0 || !subset(eps, episodes) {
			unwanted = append(unwanted, file.ID)
			continue
		}
		wanted = append(wanted, file.ID)
		for _, ep := range eps {
			if !contains(out.SelectedEpisodes, ep) {
				out.SelectedEpisodes = append(out.SelectedEpisodes, ep)
			}
		}
	}
	if kind == "qbittorrent" {
		// The source deliberately leaves all default priorities intact when no
		// filename matches. It also leaves wanted files' priorities unchanged.
		if len(out.SelectedEpisodes) > 0 && len(unwanted) > 0 {
			if _, e = d.SetFiles(ctx, unwanted, false); e != nil {
				return out, e
			}
		}
	} else {
		if _, e = d.SetFiles(ctx, wanted, true); e != nil {
			return out, e
		}
		if _, e = d.SetFiles(ctx, unwanted, false); e != nil {
			return out, e
		}
	}
	out.Started, e = d.Start(ctx, kind == "qbittorrent" && force)
	return out, e
}
