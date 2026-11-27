package sitemessageautoread

import (
	"encoding/json"
	"errors"
	"strings"

	"light-media/internal/plugins/schedule"
)

const (
	defaultCron   = "0 * * * *"
	maxTitles     = 200
	maxTitleBytes = 1024
)

type Config struct {
	Enabled   bool     `json:"enabled"`
	OnlyOnce  bool     `json:"onlyonce"`
	Cron      string   `json:"cron"`
	Sites     []string `json:"sites"`
	TitleText string   `json:"titles"`
}

// ParseConfig is also used by the host to enforce the saved site and title
// selection at the point where an authenticated request is about to run.
func ParseConfig(raw json.RawMessage) (Config, error) {
	config := Config{Cron: defaultCron, Sites: []string{}}
	if len(raw) != 0 && json.Unmarshal(raw, &config) != nil {
		return Config{}, errors.New("短讯自动已读的配置格式无效")
	}
	config.Sites = uniqueValues(config.Sites)
	config.Cron = strings.TrimSpace(config.Cron)
	if config.Cron == "" {
		config.Cron = defaultCron
	}
	titles := config.Titles()
	if len(titles) > maxTitles {
		return Config{}, errors.New("短讯标题最多可填写 200 条")
	}
	for _, title := range titles {
		if len(title) > maxTitleBytes {
			return Config{}, errors.New("每条短讯标题最多可填写 1024 字节")
		}
	}
	if config.Enabled || config.OnlyOnce {
		if len(config.Sites) == 0 {
			return Config{}, errors.New("请选择至少一个站点")
		}
		if len(titles) == 0 {
			return Config{}, errors.New("请填写至少一个完整的短讯标题")
		}
	}
	if config.Enabled {
		if _, err := schedule.ParseCrontab(config.Cron, "UTC"); err != nil {
			return Config{}, errors.New("执行周期必须是有效的五字段 Cron 表达式")
		}
	}
	return config, nil
}

// Scheduled reports whether a successfully parsed configuration enables the
// public service. One-shot execution is separately bound to a config save.
func (config Config) Scheduled() bool {
	return config.Enabled
}

func (config Config) Titles() []string {
	return uniqueValues(strings.Split(config.TitleText, "\n"))
}

func uniqueValues(values []string) []string {
	keys := make(map[string]struct{}, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if _, exists := keys[value]; value == "" || exists {
			continue
		}
		keys[value] = struct{}{}
		unique = append(unique, value)
	}
	return unique
}
