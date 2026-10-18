package app

import (
	"os"
	"strings"
	"time"
)

// MoviePilot has a settings.TZ (default Asia/Shanghai). This App has no separate
// TZ setting: the native adapter binds its process TZ, then the OS local zone.
// The source cron helper independently resolves the same process-local IANA zone.
func autoBackupHostTimezone() (string, error) {
	if value := strings.TrimPrefix(strings.TrimSpace(os.Getenv("TZ")), ":"); value != "" {
		if _, e := time.LoadLocation(value); e != nil {
			return "", bad("AUTO_BACKUP_TIMEZONE_INVALID", "宿主TZ不是有效IANA时区")
		}
		return value, nil
	}
	values := []string{time.Local.String()}
	if p, e := os.Readlink("/etc/localtime"); e == nil {
		if i := strings.LastIndex(p, "/zoneinfo/"); i >= 0 {
			values = append(values, p[i+10:])
		}
	}
	if b, e := os.ReadFile("/etc/timezone"); e == nil {
		values = append(values, strings.TrimSpace(string(b)))
	}
	for _, value := range values {
		if value != "" && value != "Local" {
			if _, e := time.LoadLocation(value); e == nil {
				return value, nil
			}
		}
	}
	return "Etc/UTC", nil
}
