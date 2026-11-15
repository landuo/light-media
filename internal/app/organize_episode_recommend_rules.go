package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/dlclark/regexp2/v2"
	"light-media/internal/pyregex"
)

// EpisodeFormatRuleTable is distinct from CustomIdentifiers in MoviePilot.
// The shared Python-regex compiler is reused, not recognition-text replacements:
// templates must describe the physical filename consumed by manual organization.
type organizeEpisodeRule struct {
	Name          string `json:"name"`
	Enabled       bool   `json:"enabled"`
	Order         int    `json:"order"`
	Pattern       string `json:"pattern"`
	MinFileSizeMB int64  `json:"min_file_size_mb"`
}

func (rule *organizeEpisodeRule) UnmarshalJSON(raw []byte) error {
	type plain organizeEpisodeRule
	value := plain{Enabled: true}
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	*rule = organizeEpisodeRule(value)
	return nil
}

type organizeEpisodeRules struct {
	Revision int64                 `json:"revision"`
	Rules    []organizeEpisodeRule `json:"rules"`
}
type organizeEpisodeRulesStored struct {
	Revision int64  `json:"revision"`
	Sealed   []byte `json:"sealed"`
}

const organizeEpisodeRulesKind = "organize_episode_rules"

func (a *App) episodeFormatRules(ctx context.Context) (organizeEpisodeRules, error) {
	out := organizeEpisodeRules{Rules: []organizeEpisodeRule{}}
	var stored organizeEpisodeRulesStored
	if err := a.Store.Get(ctx, organizeEpisodeRulesKind, "main", &stored); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, nil
		}
		return out, err
	}
	raw, err := a.pluginStore.unseal(organizeEpisodeRulesKind, "system", "main", stored.Sealed)
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal(raw, &out.Rules); err != nil {
		return out, bad("INVALID_EPISODE_RULES", "集数定位规则存储读取失败")
	}
	out.Revision = stored.Revision
	return out, nil
}

var episodeGroupName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func compileEpisodeRule(rule organizeEpisodeRule) (*regexp2.Regexp, error) {
	if strings.TrimSpace(rule.Name) == "" || len(rule.Name) > 200 || rule.Pattern == "" || len(rule.Pattern) > 4096 || rule.MinFileSizeMB < 0 || rule.MinFileSizeMB > 1<<30 {
		return nil, bad("INVALID_EPISODE_RULE", "规则名称、正则或最小文件大小无效")
	}
	re, err := pyregex.Compile(rule.Pattern, false)
	if err != nil {
		return nil, bad("INVALID_EPISODE_RULE", "规则“"+rule.Name+"”正则编译失败")
	}
	if re.GroupNumberFromName("ep") < 0 {
		return nil, bad("INVALID_EPISODE_RULE", "规则“"+rule.Name+"”须包含命名捕获组 (?P<ep>...) 或 (?<ep>...)")
	}
	for _, name := range re.GetGroupNames() {
		if name != "" && name[0] >= '0' && name[0] <= '9' {
			continue
		}
		if !episodeGroupName.MatchString(name) {
			return nil, bad("INVALID_EPISODE_RULE", "规则命名捕获组须使用英文字母、数字或下划线")
		}
	}
	return re, nil
}
func (a *App) saveEpisodeFormatRules(ctx context.Context, in organizeEpisodeRules) (organizeEpisodeRules, error) {
	if in.Revision < 0 || len(in.Rules) > 200 {
		return in, bad("INVALID_EPISODE_RULES", "集数定位规则数量或版本无效")
	}
	for i, rule := range in.Rules {
		if _, err := compileEpisodeRule(rule); err != nil {
			return in, err
		}
		// Retain user order, including stable ties, as MP's sorted(order) does.
		if rule.Order < 0 {
			return in, bad("INVALID_EPISODE_RULE", fmt.Sprintf("第%d条规则顺序无效", i+1))
		}
	}
	if in.Rules == nil {
		in.Rules = []organizeEpisodeRule{}
	}
	a.recognitionMu.Lock()
	defer a.recognitionMu.Unlock()
	current, err := a.episodeFormatRules(ctx)
	if err != nil {
		return in, err
	}
	if current.Revision != in.Revision {
		return in, bad("EPISODE_RULE_REVISION_CONFLICT", "集数定位规则已变化，请重新载入后保存")
	}
	raw, err := json.Marshal(in.Rules)
	if err != nil {
		return in, err
	}
	sealed, err := a.pluginStore.seal(organizeEpisodeRulesKind, "system", "main", raw)
	if err != nil {
		return in, err
	}
	in.Revision++
	err = a.Store.Put(ctx, organizeEpisodeRulesKind, "main", organizeEpisodeRulesStored{in.Revision, sealed})
	return in, err
}
func (a *App) episodeFormatRulesAPI(r *http.Request) (any, error) {
	if r.Method == "GET" {
		return a.episodeFormatRules(r.Context())
	}
	if r.Method != "PUT" {
		return nil, bad("METHOD_NOT_ALLOWED", "不支持此操作")
	}
	var in organizeEpisodeRules
	if err := decodeBody(nil, r, &in); err != nil {
		return nil, err
	}
	return a.saveEpisodeFormatRules(r.Context(), in)
}
func enabledEpisodeRules(rules []organizeEpisodeRule) []organizeEpisodeRule {
	result := []organizeEpisodeRule{}
	for _, rule := range rules {
		if rule.Enabled {
			result = append(result, rule)
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].Order < result[j].Order })
	return result
}
