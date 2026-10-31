package torrenttransfer

import (
	"context"
	"errors"
	"fmt"
	"math/big"
)

// LocalBackupMaterialIO is a purpose-bound component port, not a filesystem
// capability or pathname RPC. The caller selects/authorizes the exact torrent,
// optional same-name resume, and owned temporary material before providing it.
// This package never opens files, contacts downloaders, or removes anything.
type LocalBackupMaterialIO interface {
	TorrentExists(context.Context) (bool, error)
	ReadTorrent(context.Context) ([]byte, error)
	ResumeExists(context.Context) (bool, error)
	ReadResume(context.Context) ([]byte, error)
	WriteTemporary(context.Context, []byte) error
	ReadTemporary(context.Context) ([]byte, error)
}

// Labels are only the source log text; no IO operation receives these strings.
// QBittorrent must come from the caller's actual selected downloader, not a new
// inference from the filename or bytes.
type LocalBackupMaterialInput struct {
	QBittorrent                     bool
	Hash, TorrentLabel, ResumeLabel string
}

// Skipped is the source's failed-item continue, not an empty successful file.
// Temporary means the reconstructed material was actually written and reread
// through the supplied port. It does not imply unlink or whole transfer success.
type LocalBackupMaterial struct {
	Content            []byte
	Skipped, Temporary bool
}

// PrepareLocalBackupMaterial ports only TorrentTransfer 1.10.3 lines 785–864
// and the final material read at 868. Duplicate-destination checks, save-path
// conversion, downloader adds and their logs/counters are outside this component.
// The existing bounded fast-bencode codec is reused, not TorrentHelper/torrentool.
func PrepareLocalBackupMaterial(ctx context.Context, in LocalBackupMaterialInput, port LocalBackupMaterialIO, log func(Log)) (LocalBackupMaterial, error) {
	if ctx == nil || port == nil {
		return LocalBackupMaterial{}, errors.New("local backup material invocation is incomplete")
	}
	if err := ctx.Err(); err != nil {
		return LocalBackupMaterial{}, err
	}
	emit := func(line int, level, message string) {
		if log != nil {
			log(Log{Line: line, Level: level, Message: message})
		}
	}
	skip := func() (LocalBackupMaterial, error) { return LocalBackupMaterial{Skipped: true}, nil }
	exists, err := port.TorrentExists(ctx)
	if err != nil {
		return LocalBackupMaterial{}, err
	}
	if !exists {
		emit(787, "ERROR", "种子文件不存在："+in.TorrentLabel)
		return skip()
	}
	temporary := false
	if in.QBittorrent {
		// Source read is outside the decoder try: never turn this IO error into
		// a malformed-torrent WARN/skip. Even unchanged bytes are read again below.
		content, err := port.ReadTorrent(ctx)
		if err != nil {
			return LocalBackupMaterial{}, err
		}
		if err := ctx.Err(); err != nil {
			return LocalBackupMaterial{}, err
		}
		if len(content) == 0 {
			emit(821, "WARN", "读取种子文件失败："+in.TorrentLabel)
			return skip()
		}
		main, err := localBackupDictionary(content)
		if err != nil {
			emit(829, "WARN", fmt.Sprintf("解析种子文件 %s 失败：%s", in.TorrentLabel, err))
			return skip()
		}
		if !localBackupTruth(main["announce"]) {
			emit(834, "INFO", in.Hash+" 未发现tracker信息，尝试补充tracker信息...")
			exists, err = port.ResumeExists(ctx)
			if err != nil {
				return LocalBackupMaterial{}, err
			}
			if !exists {
				emit(838, "WARN", "fastresume文件不存在："+in.ResumeLabel)
				return skip()
			}
			// Only this source block catches ordinary resume read/decode/rebuild/
			// temporary-write errors. The subsequent material read is outside it.
			temporary, err = localBackupRestore(ctx, main, port)
			if err != nil {
				if ctx.Err() != nil {
					return LocalBackupMaterial{}, ctx.Err()
				}
				emit(861, "ERROR", fmt.Sprintf("解析fastresume文件 %s 出错：%s", in.ResumeLabel, err))
				return skip()
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return LocalBackupMaterial{}, err
	}
	var content []byte
	if temporary {
		content, err = port.ReadTemporary(ctx)
	} else {
		content, err = port.ReadTorrent(ctx)
	}
	if err != nil {
		return LocalBackupMaterial{}, err
	}
	if err := ctx.Err(); err != nil {
		return LocalBackupMaterial{}, err
	}
	return LocalBackupMaterial{Content: append([]byte{}, content...), Temporary: temporary}, nil
}

func localBackupDictionary(raw []byte) (map[string]any, error) {
	value, err := bdecode(raw)
	if err != nil {
		return nil, err
	}
	if dict, ok := value.(map[string]any); ok {
		return dict, nil
	}
	name := "int"
	switch value.(type) {
	case []any:
		name = "list"
	case string:
		name = "str"
	case []byte:
		name = "bytes"
	}
	return nil, fmt.Errorf("'%s' object has no attribute 'get'", name)
}

func localBackupTruth(value any) bool {
	// bdecode produces *big.Int; the existing business JSON truth helper does
	// not cover this codec type. Do not change the old Core's behavior here.
	if n, ok := value.(*big.Int); ok {
		return n.Sign() != 0
	}
	return truth(value)
}

func localBackupRestore(ctx context.Context, main map[string]any, port LocalBackupMaterialIO) (bool, error) {
	raw, err := port.ReadResume(ctx)
	if err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	resume, err := localBackupDictionary(raw)
	if err != nil {
		return false, err
	}
	trackers, ok := resume["trackers"].([]any)
	if !ok || len(trackers) == 0 || !localBackupTruth(trackers[0]) {
		return false, nil
	}
	var tracker any
	var length int
	switch first := trackers[0].(type) {
	case []any:
		length, tracker = len(first), first[0]
	case string:
		runes := []rune(first)
		length, tracker = len(runes), string(runes[0])
	case []byte:
		length, tracker = len(first), newByteInteger(first[0])
	case map[string]any:
		return false, errors.New("0") // Python dict[0] KeyError; codec dictionary keys are strings/bytes.
	default:
		return false, errors.New("'int' object is not subscriptable")
	}
	main["announce"] = tracker
	if len(trackers) > 1 || length > 1 {
		main["announce-list"] = trackers
	}
	// A single tracker deliberately leaves any old announce-list intact.
	content, err := bencode(main)
	if err != nil {
		return false, err
	}
	if err = port.WriteTemporary(ctx, content); err != nil {
		return false, err
	}
	return true, nil
}
