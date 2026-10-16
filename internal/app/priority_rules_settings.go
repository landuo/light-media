package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"

	"light-media/internal/priorityrules"
)

var priorityRuleSections = []string{"custom", "groups", "priority", "defaults"}

type priorityRuleDefaults struct {
	Search    []string           `json:"search_groups"`
	Subscribe []string           `json:"subscribe_groups"`
	Mode      priorityrules.Mode `json:"source_mode"`
}

func ruleConfiguration(config *priorityrules.Configuration) priorityrules.Configuration {
	if config == nil {
		return priorityrules.Configuration{}
	}
	return *config
}
func ruleSection(config priorityrules.Configuration, section string) any {
	switch section {
	case "custom":
		return config.CustomRules
	case "groups":
		return config.Groups
	case "priority":
		return config.TorrentPriority
	case "defaults":
		return priorityRuleDefaults{config.SearchGroups, config.SubscribeGroups, config.Mode}
	}
	return nil
}
func ruleRevision(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, section string) (int64, error) {
	var revision int64
	err := q.QueryRowContext(ctx, `SELECT revision FROM priority_rule_revisions WHERE section=?`, section).Scan(&revision)
	if err == sql.ErrNoRows {
		err = nil
	}
	return revision, err
}
func bumpPriorityRuleRevisions(ctx context.Context, tx *sql.Tx, before, after *priorityrules.Configuration) error {
	for _, section := range priorityRuleSections {
		if reflect.DeepEqual(ruleSection(ruleConfiguration(before), section), ruleSection(ruleConfiguration(after), section)) {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO priority_rule_revisions(section,revision) VALUES(?,1) ON CONFLICT(section) DO UPDATE SET revision=revision+1`, section); err != nil {
			return err
		}
	}
	return nil
}
func validateRuleSection(config priorityrules.Configuration, section string) error {
	seenID, seenName := map[string]bool{}, map[string]bool{}
	if section == "custom" {
		for _, rule := range config.CustomRules {
			id, _ := rule["id"].(string)
			name, _ := rule["name"].(string)
			if id == "" || strings.TrimSpace(name) == "" || seenID[id] || seenName[name] {
				return bad("INVALID_PRIORITY_RULES", "规则 ID 和名称不能为空或重复")
			}
			seenID[id], seenName[name] = true, true
		}
	}
	if section == "groups" {
		for _, group := range config.Groups {
			if strings.TrimSpace(group.Name) == "" || seenName[group.Name] {
				return bad("INVALID_PRIORITY_RULES", "规则组名称不能为空或重复")
			}
			seenName[group.Name] = true
		}
	}
	return nil
}
func (a *App) priorityRulesAPI(w http.ResponseWriter, r *http.Request, section string) (any, error) {
	ctx := r.Context()
	if section == "" && r.Method == "GET" {
		a.settingsMu.Lock()
		defer a.settingsMu.Unlock()
		revisions := map[string]int64{}
		for _, key := range priorityRuleSections {
			revision, err := ruleRevision(ctx, a.Store.DB, key)
			if err != nil {
				return nil, err
			}
			revisions[key] = revision
		}
		policy, err := loadMediaCategoryPolicy(a.Dir)
		if err != nil {
			return nil, err
		}
		categories := map[string][]string{"电影": {}, "电视剧": {}}
		for kind, title := range map[string]string{"movie": "电影", "tv": "电视剧"} {
			for _, rule := range policy[kind] {
				categories[title] = append(categories[title], rule.name)
			}
		}
		return map[string]any{"configuration": ruleConfiguration(a.Settings().PriorityRules), "revisions": revisions, "categories": categories}, nil
	}
	valid := false
	for _, key := range priorityRuleSections {
		valid = valid || key == section
	}
	if !valid || r.Method != "PUT" {
		return nil, &APIError{"NOT_FOUND", "接口不存在", 404}
	}
	var in struct {
		Revision *int64          `json:"revision"`
		Value    json.RawMessage `json:"value"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		return nil, err
	}
	if in.Revision == nil || *in.Revision < 0 || len(in.Value) == 0 {
		return nil, bad("INVALID_PRIORITY_RULES", "缺少规则内容或保存版本")
	}
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	c := a.Settings()
	config := ruleConfiguration(c.PriorityRules)
	var err error
	switch section {
	case "custom":
		err = json.Unmarshal(in.Value, &config.CustomRules)
	case "groups":
		err = json.Unmarshal(in.Value, &config.Groups)
	case "priority":
		err = json.Unmarshal(in.Value, &config.TorrentPriority)
	case "defaults":
		var defaults priorityRuleDefaults
		err = json.Unmarshal(in.Value, &defaults)
		config.SearchGroups, config.SubscribeGroups, config.Mode = defaults.Search, defaults.Subscribe, defaults.Mode
	}
	if err != nil {
		return nil, bad("INVALID_PRIORITY_RULES", "规则内容格式不正确")
	}
	if err := validateRuleSection(config, section); err != nil {
		return nil, err
	}
	c.PriorityRules = &config
	err = a.saveSettingsWithEffect(ctx, c, func(ctx context.Context, tx *sql.Tx, _ *Settings) error {
		revision, err := ruleRevision(ctx, tx, section)
		if err != nil {
			return err
		}
		if revision != *in.Revision {
			return &APIError{"PRIORITY_RULE_CONFLICT", "此分区已被其他窗口修改，请刷新后重新编辑；当前草稿仍保留", 409}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	revision, err := ruleRevision(ctx, a.Store.DB, section)
	return map[string]any{"value": ruleSection(config, section), "revision": revision}, err
}
