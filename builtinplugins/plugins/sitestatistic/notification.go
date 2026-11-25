package sitestatistic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
)

func BuildNotification(selected Selected, mode any, today string) (notice *Notification, err error) {
	if !truth(mode) {
		return nil, nil
	}
	return buildNotification(selected, func() any { return mode }, today)
}
func buildNotification(selected Selected, mode func() any, today string) (notice *Notification, err error) {
	defer recoverError(&err)
	current, previous := map[string]Data{}, map[string]Data{}
	order := []string{}
	for _, d := range selected.Current {
		k := dataKey(d)
		if _, known := current[k]; !known {
			order = append(order, k)
		}
		current[k] = d
	}
	for _, d := range selected.Previous {
		previous[dataKey(d)] = d
	}
	approx := Approximate(selected.Current, selected.Previous)
	anyApprox := map[string]bool{}
	type message struct {
		key  float64
		text string
	}
	messages := []message{}
	uploadTotal, downloadTotal := new(big.Int), new(big.Int)
	for index, key := range order {
		d := current[key]
		upload, download := integer(d["upload"]), integer(d["download"])
		if sourceEqual(mode(), "inc") {
			p := previous[key]
			if p == nil {
				continue
			}
			pu, pd := integer(p["upload"]), integer(p["download"])
			if pu.Sign() == 0 && pd.Sign() == 0 {
				continue
			}
			upload.Sub(upload, pu)
			download.Sub(download, pd)
			if upload.Sign() < 0 {
				upload.SetInt64(0)
			}
			if download.Sign() < 0 {
				download.SetInt64(0)
			}
		}
		if upload.Sign() <= 0 && download.Sign() <= 0 && len(approx[key]) == 0 {
			continue
		}
		uploadTotal.Add(uploadTotal, upload)
		downloadTotal.Add(downloadTotal, download)
		for field := range approx[key] {
			anyApprox[field] = true
		}
		updated := ""
		if truth(d["updated_day"]) && !sourceEqual(d["updated_day"], today) {
			updated = "（" + pyText(d["updated_day"]) + "）"
		}
		body := "【" + pyText(d["name"]) + "】" + updated + "\n上传量：" + Traffic(upload, approx[key]["upload"], sourceEqual(mode(), "inc")) + "\n下载量：" + Traffic(download, approx[key]["download"], sourceEqual(mode(), "inc")) + "\n————————————"
		f, _ := newFloat(upload)
		if math.IsInf(f, 0) {
			return nil, errors.New("int too large to convert to float")
		}
		sortKey := f + float64(index)/1000
		replaced := false
		for i := range messages {
			if messages[i].key == sortKey {
				messages[i].text = body
				replaced = true
				break
			}
		}
		if !replaced {
			messages = append(messages, message{sortKey, body})
		}
	}
	if len(messages) == 0 {
		return nil, nil
	}
	sort.Slice(messages, func(i, j int) bool { return messages[i].key > messages[j].key })
	parts := []string{"【汇总】\n总上传：" + Traffic(uploadTotal, anyApprox["upload"], sourceEqual(mode(), "inc")) + "\n总下载：" + Traffic(downloadTotal, anyApprox["download"], sourceEqual(mode(), "inc")) + "\n————————————"}
	for _, m := range messages {
		parts = append(parts, m.text)
	}
	if len(anyApprox) > 0 {
		parts = append(parts, PrecisionNote)
	}
	body := strings.Join(parts, "\n")
	digest := sha256.Sum256([]byte(body))
	return &Notification{Title: "站点数据统计", Text: body, Fingerprint: hex.EncodeToString(digest[:]), uploadTotal: uploadTotal, downloadTotal: downloadTotal}, nil
}
func sourceEqual(a, b any) bool {
	if isNumeric(a) && isNumeric(b) {
		ar, br := numericRat(a), numericRat(b)
		if ar != nil && br != nil {
			return ar.Cmp(br) == 0
		}
		af, e := numeric(a)
		bf, f := numeric(b)
		return e == nil && f == nil && af == bf
	}
	if am, ok := a.(map[string]any); ok {
		bm, ok := b.(map[string]any)
		if !ok || len(am) != len(bm) {
			return false
		}
		for k, v := range am {
			bv, exists := bm[k]
			if !exists || !sourceEqual(v, bv) {
				return false
			}
		}
		return true
	}
	if as, ok := a.([]any); ok {
		bs, ok := b.([]any)
		if !ok || len(as) != len(bs) {
			return false
		}
		for i, v := range as {
			if !sourceEqual(v, bs[i]) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(a, b)
}
func isNumeric(v any) bool {
	switch v.(type) {
	case bool, float64, int, int64, *big.Int, json.Number:
		return true
	}
	return false
}
func numericRat(v any) *big.Rat {
	if n, ok := v.(*big.Int); ok {
		return new(big.Rat).SetInt(n)
	}
	if n, ok := v.(json.Number); ok && !strings.ContainsAny(string(n), ".eE") {
		r, ok := new(big.Rat).SetString(string(n))
		if ok {
			return r
		}
	}
	f, e := numeric(v)
	if e != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return nil
	}
	return new(big.Rat).SetFloat64(f)
}

// The module lock guards only the original get/post/save critical section.
// Reads and arithmetic run before it, and failure never creates a receipt.
func sendSelected(ctx context.Context, h Host, selected Selected, mode func() any, now func() time.Time, lock *sync.Mutex) error {
	today := now().Format("2006-01-02")
	notice, e := buildNotification(selected, mode, today)
	if e != nil || notice == nil {
		return e
	}
	lock.Lock()
	defer lock.Unlock()
	last, e := h.GetData(ctx, "last_notify")
	if e != nil {
		return e
	}
	if !truth(last) {
		last = map[string]any{}
	}
	fields, ok := last.(map[string]any)
	if !ok {
		return errors.New("last_notify object has no attribute 'get'")
	}
	if sourceEqual(fields["date"], today) && sourceEqual(fields["type"], mode()) && sourceEqual(fields["fingerprint"], notice.Fingerprint) {
		return h.Log(ctx, "INFO", fmt.Sprintf("站点数据统计通知内容未变化，跳过本次重复通知（%s），本次增量：上传 %s，下载 %s", today, FormatSize(notice.uploadTotal), FormatSize(notice.downloadTotal)))
	}
	if e = h.Post(ctx, *notice); e != nil {
		return e
	}
	if e = h.SaveData(ctx, "last_notify", map[string]any{"date": today, "type": mode(), "fingerprint": notice.Fingerprint, "time": now().Format("15:04:05")}); e != nil {
		return e
	}
	return h.Log(ctx, "INFO", fmt.Sprintf("站点数据统计通知发送完成（%s），总上传 %s，总下载 %s", today, FormatSize(notice.uploadTotal), FormatSize(notice.downloadTotal)))
}
