package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/sys/unix"
	"light-media/internal/plugins/builtin"
)

func (a *App) containerHostsPath() string {
	if a.pluginHostsFile != "" {
		return a.pluginHostsFile
	}
	return "/etc/hosts"
}

func hostsDigest(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func readHostsFile(file *os.File) ([]byte, error) {
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > builtin.MaxCustomHostsBytes {
		return nil, bad("HOSTS_FILE_UNSAFE", "容器hosts必须是不超过128 KiB的普通文件")
	}
	raw, err := io.ReadAll(io.LimitReader(file, builtin.MaxCustomHostsBytes+1))
	if err != nil || len(raw) > builtin.MaxCustomHostsBytes || !utf8.Valid(raw) || strings.IndexByte(string(raw), 0) >= 0 {
		return nil, bad("HOSTS_READ_FAILED", "无法读取有效的容器hosts文件")
	}
	return raw, nil
}

func (a *App) readContainerHosts() (json.RawMessage, error) {
	fd, err := unix.Open(a.containerHostsPath(), unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, bad("HOSTS_READ_FAILED", "无法读取容器hosts文件，请检查挂载及权限")
	}
	file := os.NewFile(uintptr(fd), "container-hosts")
	defer file.Close()
	raw, err := readHostsFile(file)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]string{"content": string(raw), "digest": hostsDigest(raw)})
}

func writeHostsFile(file *os.File, raw []byte) error {
	n, err := file.WriteAt(raw, 0)
	if err != nil || n != len(raw) {
		return errors.New("hosts write failed")
	}
	if err := file.Truncate(int64(len(raw))); err != nil {
		return err
	}
	return file.Sync()
}

// A Docker /etc/hosts bind mount cannot be atomically renamed. Keep the system
// prefix byte-for-byte, write before truncating, lock/check the same fd and
// restore the old bytes on an I/O error. A restart can reapply the managed tail.
func (a *App) replaceContainerHosts(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var in struct {
		Content        string `json:"content"`
		ExpectedDigest string `json:"expected_digest"`
	}
	if json.Unmarshal(raw, &in) != nil || len(in.Content) > builtin.MaxCustomHostsBytes || !utf8.ValidString(in.Content) || strings.IndexByte(in.Content, 0) >= 0 {
		return nil, bad("INVALID_HOSTS_VALUE", "hosts内容无效或超过128 KiB")
	}
	fd, err := unix.Open(a.containerHostsPath(), unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, bad("HOSTS_WRITE_FAILED", "无法写入容器hosts，请单独提供该文件的可写挂载并检查文件权限")
	}
	file := os.NewFile(uintptr(fd), "container-hosts")
	defer file.Close()
	if unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB) != nil {
		return nil, bad("HOSTS_BUSY", "容器hosts正在被其他操作修改，请重试")
	}
	defer unix.Flock(fd, unix.LOCK_UN)
	before, err := readHostsFile(file)
	if err != nil {
		return nil, err
	}
	if hostsDigest(before) != in.ExpectedDigest {
		return nil, bad("HOSTS_CHANGED", "容器hosts已变化，未覆盖其他修改，请重新加载插件")
	}
	base := builtin.CustomHostsBase(string(before))
	if !strings.HasPrefix(in.Content, base) {
		return nil, bad("HOSTS_SCOPE_DENIED", "插件不能修改自身标记之前的系统hosts内容")
	}
	tail := strings.TrimPrefix(in.Content, base)
	if tail != "" && !strings.HasPrefix(strings.TrimPrefix(tail, "\n"), builtin.CustomHostsMarker+"\n") {
		return nil, bad("HOSTS_SCOPE_DENIED", "插件只能替换自身标记下的hosts记录")
	}
	if ctx.Err() != nil {
		return nil, bad("PLUGIN_CANCELED", "插件执行已取消")
	}
	if err := writeHostsFile(file, []byte(in.Content)); err != nil {
		if restoreErr := writeHostsFile(file, before); restoreErr != nil {
			return nil, bad("HOSTS_RECOVERY_REQUIRED", "hosts写入及恢复均失败，需要核对该容器文件后重新加载插件")
		}
		return nil, bad("HOSTS_WRITE_FAILED", "hosts写入失败，已恢复原文件")
	}
	return json.RawMessage(`{"applied":true}`), nil
}
