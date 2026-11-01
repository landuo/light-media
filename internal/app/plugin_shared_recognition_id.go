package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
)

// originalRecognitionIDPorts 只表示固定 TMDB get_info 读取端口。
// GetInfo 的非空返回是已完成 provider augmentation 的完整字典，不是 UI Media。
// 此端口不是 worker 可提供的回调，也不允许自选 URL、凭据或其它 provider。
type originalRecognitionIDPorts struct {
	GetInfo func(context.Context, string, int64) (json.RawMessage, error)
	Log     func(level, message string)
}

// originalRecognitionIDInfo 对应原 _get_info_by_tmdbid 的 meta=None 分支。
// mtype 空串仅表示源 None；本小块只支持电影/电视剧枚举，不把 UNKNOWN 合并为 None。
// 本函数不构造 MediaInfo，因此双命中时不会提前投影/分类被丢弃的任一结果。
func originalRecognitionIDInfo(ctx context.Context, id int64, mtype string, ports originalRecognitionIDPorts) (json.RawMessage, error) {
	if ctx == nil || ports.GetInfo == nil || ports.Log == nil || id <= 0 || id > 1<<31-1 {
		return nil, bad("SOURCE_RECOGNITION_ID_UNSUPPORTED", "显式TMDB识别输入超出当前原生端口范围")
	}
	if mtype != "" && mtype != "电影" && mtype != "电视剧" {
		return nil, bad("SOURCE_RECOGNITION_ID_UNSUPPORTED", "当前显式TMDB类型尚未适配")
	}
	read := func(kind string) (json.RawMessage, bool, error) {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		raw, err := ports.GetInfo(ctx, kind, id)
		if err != nil {
			return nil, false, err
		}
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return raw, false, nil
		}
		value, err := decodeSourceJSON(raw)
		if err != nil || sourceMap(value) == nil {
			return nil, false, bad("SOURCE_RECOGNITION_INFO_UNSUPPORTED", "TMDB读取端口未返回完整字典")
		}
		return raw, len(sourceMap(value)) != 0, nil
	}
	if mtype != "" {
		raw, _, err := read(mtype)
		return raw, err
	}
	tv, tvPresent, err := read("电视剧")
	if err != nil {
		return nil, err
	}
	movie, moviePresent, err := read("电影")
	if err != nil {
		return nil, err
	}
	if tvPresent && moviePresent {
		ports.Log("WARN", fmt.Sprintf("无法判断tmdb_id:%d 是电影还是电视剧", id))
		return nil, nil
	}
	if tvPresent {
		return tv, nil
	}
	if moviePresent {
		return movie, nil
	}
	return nil, nil
}
