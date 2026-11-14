package app

import (
	"context"
	"path/filepath"
)

func organizeFileHistorySource(file importScanFile) string {
	if file.queueHistory != nil {
		return file.queueHistory.ResourceSource
	}
	return ""
}

// MP submits each selected history independently. Resolve the stable aliases in
// one read, then publish all candidates before inspecting any prior plan or
// recognizing media. Existing history IDs remain attached to the source.
func (a *App) prepareOrganizeHistory(ctx context.Context, job Job, in manualOrganizeRequest) (manualOrganizeOutput, error) {
	out := manualOrganizeOutput{Items: []manualOrganizeItem{}}
	histories, err := a.transferHistorySelections(ctx, in.HistoryIDs)
	if err != nil {
		return out, err
	}
	files := []importScanFile{}
	seen := map[string]bool{}
	for _, history := range histories {
		if history.ID == "" {
			out.Failed++
			out.Items = append(out.Items, manualOrganizeItem{Status: "failed", Error: "所选整理历史已不存在，其余记录继续处理"})
			continue
		}
		if seen[history.ResourceSource] {
			continue
		}
		seen[history.ResourceSource] = true
		path := history.Source
		if history.Status == "success" && history.Mode == "move" && history.Target != "" {
			path = history.Target
		}
		files = append(files, importScanFile{Path: path, Size: history.Size, Parsed: a.parseMediaName(filepath.Base(path)), queueHistory: &history})
	}
	return a.enqueueOrganizeFiles(ctx, job, files, nil, out)
}
