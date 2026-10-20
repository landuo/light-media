// Package transferoverwrite ports the installed file manager's per-file policy.
// It does not grant filesystem permissions or infer a policy from quality rank.
package transferoverwrite

import (
	"context"
	"errors"
	"math/big"
)

const EventType = "transfer.overwrite.check"

var ErrDependency = errors.New("overwrite policy or synchronous event dependency is unknown")

type Policy struct {
	Known bool    `json:"known"`
	Mode  *string `json:"mode"`
}

// FileItem uses the original attribute names. Stat sizes are never synthesized
// as zero; extension excludes the dot, as in schemas.FileItem.
type FileItem struct {
	Path         string   `json:"path"`
	Storage      string   `json:"storage"`
	Type         string   `json:"type"`
	Name         string   `json:"name"`
	Basename     string   `json:"basename"`
	Extension    string   `json:"extension"`
	Size         *big.Int `json:"size"`
	ModifyTime   *float64 `json:"modify_time"`
	Children     []any    `json:"children"`
	FileID       *string  `json:"fileid"`
	ParentFileID *string  `json:"parent_fileid"`
	Thumbnail    *string  `json:"thumbnail"`
	Pickcode     *string  `json:"pickcode"`
	DriveID      *string  `json:"drive_id"`
	URL          *string  `json:"url"`
}
type Event struct {
	FileItem      FileItem       `json:"fileitem"`
	TargetItem    FileItem       `json:"target_item"`
	TargetStorage string         `json:"target_storage"`
	TargetPath    string         `json:"target_path"`
	OverwriteMode string         `json:"overwrite_mode"`
	TransferType  string         `json:"transfer_type"`
	Options       map[string]any `json:"options"`
	SourceSize    *big.Int       `json:"source_size"`
	TargetSize    *big.Int       `json:"target_size"`
	Overwrite     *bool          `json:"overwrite"`
	Source        string         `json:"source"`
	Reason        string         `json:"reason"`
}
type Dispatch func(context.Context, Event) (*Event, error)
type Input struct {
	Policy           Policy    `json:"policy"`
	Source           FileItem  `json:"source"`
	Target           *FileItem `json:"target"`
	TargetKnown      bool      `json:"target_known"`
	TargetQueryError string    `json:"target_query_error"`
	TargetPath       string    `json:"target_path"`
	TargetStorage    string    `json:"target_storage"`
	TransferType     string    `json:"transfer_type"`
	Extra            bool      `json:"extra"`
	// BrokenTargetLink is only relevant when strict storage returned an item.
	// A dangling link alone is absent according to LocalStorage.get_item_strict.
	BrokenTargetLink bool `json:"broken_target_link"`
}
type Decision struct {
	Allowed        bool   `json:"allowed"`
	Overwrite      bool   `json:"overwrite"`
	DeleteVersions bool   `json:"delete_versions"`
	EventRequired  bool   `json:"event_required"`
	Reason         string `json:"reason"`
	SourceError    string `json:"source_error"`
}

func Decide(ctx context.Context, in Input, dispatch Dispatch) (Decision, error) {
	d := Decision{}
	if err := ctx.Err(); err != nil {
		return d, err
	}
	if in.Extra {
		d.Allowed = true
		d.Overwrite = true
		return d, nil
	}
	if in.TargetQueryError != "" {
		d.Reason = "无法确认目标文件状态，已跳过整理以避免误覆盖：" + in.TargetPath + " - " + in.TargetQueryError
		return d, nil
	}
	if !in.TargetKnown {
		return d, ErrDependency
	}
	mode := ""
	if in.Policy.Mode != nil {
		mode = *in.Policy.Mode
	}
	if in.Target == nil {
		d.Allowed = true
		d.DeleteVersions = in.Policy.Known && mode == "latest"
		return d, nil
	}
	if in.TargetStorage == "local" && in.BrokenTargetLink {
		d.Allowed = true
		d.Overwrite = true
		return d, nil
	}
	if !in.Policy.Known || dispatch == nil {
		return d, ErrDependency
	}
	d.EventRequired = true
	ev := Event{FileItem: in.Source, TargetItem: *in.Target, TargetStorage: in.TargetStorage, TargetPath: in.TargetPath, OverwriteMode: mode, TransferType: in.TransferType, Source: "未知处理源"}
	result, err := dispatch(ctx, ev)
	if err != nil {
		return d, err
	}
	var override *bool
	var sourceSize, targetSize *big.Int
	var reason string
	if result != nil {
		override = result.Overwrite
		sourceSize = result.SourceSize
		targetSize = result.TargetSize
		reason = result.Reason
	}
	if override != nil {
		if *override {
			d.Allowed = true
			d.Overwrite = true
			return d, nil
		}
		d.Reason = reason
		if d.Reason == "" {
			d.Reason = "插件决定不覆盖已有文件"
		}
		return d, nil
	}
	switch mode {
	case "always", "latest":
		d.Allowed = true
		d.Overwrite = true
	case "size":
		if sourceSize == nil {
			sourceSize = in.Source.Size
		}
		if targetSize == nil {
			targetSize = in.Target.Size
		}
		if sourceSize == nil || targetSize == nil {
			left, right := "int", "int"
			if targetSize == nil {
				left = "NoneType"
			}
			if sourceSize == nil {
				right = "NoneType"
			}
			d.SourceError = "TypeError"
			d.Reason = "'<' not supported between instances of '" + left + "' and '" + right + "'"
			return d, nil
		}
		if targetSize.Cmp(sourceSize) < 0 {
			d.Allowed = true
			d.Overwrite = true
		} else {
			d.Reason = "媒体库存在同名文件，且质量更好"
		}
	case "never":
		d.Reason = "媒体库存在同名文件，当前覆盖模式为不覆盖"
	default:
		d.Reason = in.TargetPath + " 已存在"
	}
	return d, nil
}

// VersionMatches is the original latest cleanup's comparison. Filename parsing
// is the host's trusted MetaInfoPath responsibility, including None vs zero.
type Identity struct {
	Season  string  `json:"season"`
	Episode string  `json:"episode"`
	Part    *string `json:"part"`
}

func VersionMatches(want, old Identity) bool {
	if want.Season != old.Season || want.Episode != old.Episode {
		return false
	}
	return want.Part == nil || *want.Part == "" || old.Part == nil || *old.Part == "" || *want.Part == *old.Part
}
