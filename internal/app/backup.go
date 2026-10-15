package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Bootstrap is a one-time CLI operation, with credentials supplied through
// stdin; the HTTP API never accepts unencrypted configuration imports.
func Bootstrap(dir string, r io.Reader) error {
	a, e := New(Options{DataDir: dir})
	if e != nil {
		return e
	}
	defer a.Close()
	var in struct {
		Settings Settings `json:"settings"`
		Username string   `json:"username"`
		Password string   `json:"password"`
	}
	d := json.NewDecoder(io.LimitReader(r, 2<<20))
	if e = d.Decode(&in); e != nil {
		return errors.New("invalid bootstrap input")
	}
	if a.hasAdmin() {
		return errors.New("bootstrap is only allowed before administrator creation")
	}
	if len(in.Username) < 1 || len(in.Username) > 64 || len(in.Password) < 10 || len(in.Password) > 72 {
		return errors.New("administrator credentials do not meet length requirements")
	}
	if e = a.saveSettings(context.Background(), in.Settings); e != nil {
		return e
	}
	return a.bootstrapAdmin(in.Username, in.Password)
}
func (a *App) prepareRestore(ctx context.Context, name string, confirm bool) (any, error) {
	if !confirm {
		return nil, bad("CONFIRM_REQUIRED", "恢复会替换应用数据库，请确认已备份并暂停自动化")
	}
	if !a.paused() {
		return nil, bad("PAUSE_REQUIRED", "请先暂停自动化后再准备恢复")
	}
	a.controlMu.Lock()
	active := a.activeWrites
	a.controlMu.Unlock()
	if active != 0 {
		return nil, bad("WRITES_ACTIVE", "仍有操作收尾，请稍后重试")
	}
	if filepath.Base(name) != name || !strings.HasSuffix(name, ".db") {
		return nil, bad("INVALID_BACKUP", "备份名称无效")
	}
	backup := filepath.Join(a.Dir, "backups", name)
	if e := checkBackup(backup); e != nil {
		return nil, e
	}
	if _, e := a.Store.Backup(ctx, filepath.Join(a.Dir, "backups")); e != nil {
		return nil, e
	}
	b, _ := json.Marshal(map[string]string{"filename": name, "prepared_at": now()})
	p := filepath.Join(a.Dir, "restore.request")
	f, e := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return nil, bad("RESTORE_PENDING", "已有恢复请求，请重启应用完成或检查")
	}
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	f.Close()
	if e != nil {
		return nil, e
	}
	a.log("WARN", "backup", "restore_prepared", "恢复请求已准备，重启后自动验证恢复并保持暂停", "", "")
	select {
	case a.RestartRequested <- struct{}{}:
	default:
	}
	return map[string]any{"status": "restarting", "message": "备份已校验，服务将自动重启恢复并保持暂停。恢复后请重新登录并核对外部状态。"}, nil
}
func checkBackup(p string) error {
	st, e := os.Lstat(p)
	if e != nil || !st.Mode().IsRegular() {
		return bad("INVALID_BACKUP", "备份文件不存在或类型无效")
	}
	u := url.URL{Scheme: "file", Path: p}
	u.RawQuery = "mode=ro"
	db, e := sql.Open("sqlite", u.String())
	if e != nil {
		return bad("INVALID_BACKUP", "无法打开备份")
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var result string
	if e = db.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&result); e != nil || result != "ok" {
		return bad("INVALID_BACKUP", "备份完整性检查失败")
	}
	var version int
	if e = db.QueryRowContext(ctx, `SELECT MAX(version) FROM migrations`).Scan(&version); e != nil || version != 1 {
		return bad("SCHEMA_MISMATCH", "备份数据库版本不兼容")
	}
	return nil
}

// ApplyPendingRestore must run before opening the application database and while
// the process owns its data-directory lock. The snapshot is immutable VACUUM INTO.
func ApplyPendingRestore(dir string) error {
	p := filepath.Join(dir, "restore.request")
	b, e := os.ReadFile(p)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	var in struct {
		Filename string `json:"filename"`
	}
	if json.Unmarshal(b, &in) != nil || filepath.Base(in.Filename) != in.Filename || !strings.HasSuffix(in.Filename, ".db") {
		return errors.New("invalid pending restore")
	}
	source := filepath.Join(dir, "backups", in.Filename)
	if e = checkBackup(source); e != nil {
		return e
	}
	key, e := os.ReadFile(filepath.Join(dir, "secret.key"))
	if e != nil {
		return errors.New("restore encryption key is missing")
	}
	u := url.URL{Scheme: "file", Path: source}
	u.RawQuery = "mode=ro"
	db, e := sql.Open("sqlite", u.String())
	if e != nil {
		return e
	}
	var cfg []byte
	e = db.QueryRow(`SELECT payload FROM records WHERE kind='config' AND id='main'`).Scan(&cfg)
	db.Close()
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	if len(cfg) > 0 {
		var c Settings
		s := Store{key: key}
		if e = s.Unseal(cfg, &c); e != nil {
			return errors.New("backup does not match application encryption key")
		}
	}
	tmp := filepath.Join(dir, "restore-"+id("")+".tmp")
	src, e := os.Open(source)
	if e != nil {
		return e
	}
	defer src.Close()
	dst, e := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	_, e = io.Copy(dst, src)
	if e == nil {
		e = dst.Sync()
	}
	dst.Close()
	if e != nil {
		os.Remove(tmp)
		return e
	}
	target := filepath.Join(dir, "light-media.db")
	for _, suffix := range []string{"-wal", "-shm"} {
		if e = os.Remove(target + suffix); e != nil && !os.IsNotExist(e) {
			os.Remove(tmp)
			return e
		}
	}
	if e = os.Rename(tmp, target); e != nil {
		return e
	}
	newDB, e := sql.Open("sqlite", target)
	if e != nil {
		return e
	}
	defer newDB.Close()
	paused, _ := json.Marshal(map[string]string{"state": "paused"})
	_, e = newDB.Exec(`INSERT INTO records VALUES('control','global',?,?) ON CONFLICT(kind,id) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at`, paused, now())
	if e != nil {
		return e
	}
	if _, e = newDB.Exec(`DELETE FROM sessions; UPDATE jobs SET state='waiting_paused',generation=generation+1 WHERE state IN ('queued','running','retry_wait');`); e != nil {
		return e
	}
	return os.Rename(p, p+".applied-"+time.Now().UTC().Format("20060102T150405"))
}
