package app

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"

	"light-media/internal/releasemeta/legacyanime"
	"light-media/internal/transferextensions"
)

// These are the actual installed TransferChain.SUBTITLE_STEM_TAGS, not arbitrary
// suffixes. Audio associations require the exact main stem.
var sourceSubtitleStemTags = map[string]bool{"cc": true, "chi": true, "chs": true, "cht": true, "cn": true, "default": true, "en": true, "eng": true, "english": true, "forced": true, "gb": true, "gb2312": true, "hk": true, "ja": true, "jap": true, "japanese": true, "jp": true, "jpn": true, "sc": true, "sdh": true, "tc": true, "zh": true, "zh-cn": true, "zh-hans": true, "zh-hant": true, "zh-tw": true, "zh_cn": true, "zh_hans": true, "zh_hant": true, "zh_tw": true, "zho": true, "中英": true, "中字": true, "双语": true, "简中": true, "简体": true, "繁中": true, "繁体": true}

func sourceExtraStem(name string, subtitle bool) string {
	ext := *transferextensions.PathFile(name).Extension
	stem := legacyanime.LowerCase(strings.TrimSuffix(filepath.Base(name), "."+ext))
	if !subtitle {
		return stem
	}
	for {
		dot := strings.LastIndexByte(stem, '.')
		if dot < 0 || !sourceSubtitleStemTags[stem[dot+1:]] {
			return stem
		}
		stem = stem[:dot]
	}
}
func sourceRelatedMainName(extra string, mains []string, constructor transferextensions.Classifier) (string, bool) {
	item := transferextensions.PathFile(extra)
	if !constructor.Extra(item) {
		return "", false
	}
	stem := sourceExtraStem(extra, constructor.Subtitle(item))
	matched := ""
	count := 0
	for _, main := range mains {
		if mainStem := sourceExtraStem(main, false); mainStem != "" && mainStem == stem {
			matched = main
			count++
		}
	}
	return matched, count == 1
}
func (a *App) sourceImportSidecars(ctx context.Context, file PlanFile, allowOverwrite bool) ([]importSidecar, error) {
	var existing func(string) error
	if allowOverwrite {
		existing = func(string) error { return nil }
	}
	return a.sourceImportSidecarsChecked(ctx, file, existing)
}
func (a *App) sourceImportSidecarsChecked(ctx context.Context, file PlanFile, existing func(string) error) ([]importSidecar, error) {
	observed := transferExtensionObservation(ctx)
	if observed == nil {
		var e error
		_, observed, e = a.observeTransferExtensions(ctx)
		if e != nil {
			return nil, e
		}
	}
	if !validTransferExtensionObservation(observed) {
		return nil, bad("REVIEW_TRANSFER_EXTENSIONS", "旁车扩展名来源不可认证")
	}
	constructor, e := transferextensions.New(observed.Constructor)
	if e != nil {
		return nil, e
	}
	handler, e := transferextensions.New(observed.Handler)
	if e != nil {
		return nil, e
	}
	dir, e := importDir(filepath.Dir(file.Source), false)
	if e != nil {
		return nil, e
	}
	defer dir.Close()
	entries, e := dir.ReadDir(2001)
	if e != nil && e != io.EOF {
		return nil, e
	}
	if len(entries) > 2000 {
		return nil, bad("REVIEW_SIDECARS", "源目录过大，请缩小范围")
	}
	mains := []string{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		media, _ := constructor.Media(transferextensions.PathFile(entry.Name()))
		if media {
			mains = append(mains, entry.Name())
		}
	}
	targetStem := strings.TrimSuffix(file.Target, filepath.Ext(file.Target))
	result := []importSidecar{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		item := transferextensions.PathFile(entry.Name())
		if !constructor.Extra(item) {
			continue
		}
		subtitle := constructor.Subtitle(item)
		if matched, ok := sourceRelatedMainName(entry.Name(), mains, constructor); !ok || matched != filepath.Base(file.Source) {
			continue
		}
		// A changed live handler can no longer classify a cached constructor extra.
		// Native preview refuses the inconsistent material instead of calling it video.
		if !handler.Extra(item) {
			return nil, bad("REVIEW_TRANSFER_EXTENSIONS", "传输实例和当前附加文件分类不一致")
		}
		name := filepath.Join(filepath.Dir(file.Source), entry.Name())
		locked, e := snapshotPath(name)
		if e != nil || locked.Size > 64<<20 {
			return nil, bad("REVIEW_SIDECARS", "附加文件无效或超过当前原生64MiB边界")
		}
		sideStem := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		if subtitle {
			for {
				dot := strings.LastIndexByte(sideStem, '.')
				if dot < 0 || !sourceSubtitleStemTags[legacyanime.LowerCase(sideStem[dot+1:])] {
					break
				}
				sideStem = sideStem[:dot]
			}
		}
		suffix := entry.Name()[len(sideStem):]
		target := targetStem + suffix
		if _, e = os.Lstat(target); e == nil {
			if existing == nil {
				return nil, bad("IMPORT_CONFLICT", "附加文件目标已存在")
			}
			if e = existing(target); e != nil {
				return nil, e
			}
		} else if !os.IsNotExist(e) {
			return nil, e
		}
		kind := "audio"
		if handler.Subtitle(item) {
			kind = "subtitle"
		}
		// Leave the old subtitle JSON shape unchanged for existing proof readers.
		if kind == "subtitle" {
			kind = ""
		}
		result = append(result, importSidecar{Source: name, Target: target, Snapshot: locked, Kind: kind})
		if len(result) > 32 {
			return nil, bad("REVIEW_SIDECARS", "附加文件超过当前原生32个边界")
		}
	}
	return result, nil
}
