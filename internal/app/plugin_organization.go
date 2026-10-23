package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strings"
	"unicode"
)

// MoviePilot's folders are global, while PluginOrder belongs to the user. This
// application currently has exactly one administrator (admin.id=1). Keep the
// two source values separate inside one encrypted, revisioned transaction.
type PluginOrganization struct {
	Revision int64                      `json:"revision"`
	Folders  map[string]json.RawMessage `json:"folders"`
	Order    json.RawMessage            `json:"order"`
	// The original user configuration distinguishes an unset PluginOrder
	// (null) from an explicitly saved empty list. Keep that private metadata
	// while the native UI always receives a usable list.
	orderPresent bool
}

const pluginOrganizationSchema = `CREATE TABLE IF NOT EXISTS plugin_organization(
 owner_id INTEGER PRIMARY KEY CHECK(owner_id=1),revision INTEGER NOT NULL CHECK(revision>0),sealed BLOB NOT NULL,updated_at TEXT NOT NULL);`

type pluginOrganizationReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (p *PluginStore) organization(ctx context.Context, db pluginOrganizationReader) (PluginOrganization, error) {
	out := PluginOrganization{Folders: map[string]json.RawMessage{}, Order: json.RawMessage(`[]`)}
	var sealed []byte
	err := db.QueryRowContext(ctx, `SELECT revision,sealed FROM plugin_organization WHERE owner_id=1`).Scan(&out.Revision, &sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	raw, err := p.unseal("organization", "admin", "main", sealed)
	if err != nil {
		return out, err
	}
	revision := out.Revision
	var stored struct {
		PluginOrganization
		OrderPresent *bool `json:"order_present"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		return out, err
	}
	out = stored.PluginOrganization
	// All rows created before this field were written by the native complete
	// organization endpoint, which always explicitly saved Order.
	out.orderPresent = stored.OrderPresent == nil || *stored.OrderPresent
	out.Revision = revision
	return out, nil
}

func (p *PluginStore) Organization(ctx context.Context) (PluginOrganization, error) {
	return p.organization(ctx, p.store.DB)
}

func pluginFolderMembers(raw json.RawMessage) ([]string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, bad("INVALID_PLUGIN_FOLDER", "文件夹内容无效")
	}
	var members []string
	if bytes.TrimSpace(raw)[0] == '[' {
		if err := json.Unmarshal(raw, &members); err != nil {
			return nil, bad("INVALID_PLUGIN_FOLDER", "文件夹成员必须是插件编号列表")
		}
	} else {
		var object map[string]json.RawMessage
		if json.Unmarshal(raw, &object) != nil || object == nil {
			return nil, bad("INVALID_PLUGIN_FOLDER", "文件夹必须使用原版列表或对象格式")
		}
		if value, exists := object["plugins"]; exists {
			if json.Unmarshal(value, &members) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return nil, bad("INVALID_PLUGIN_FOLDER", "文件夹成员必须是插件编号列表")
			}
		}
	}
	return members, nil
}

func validatePluginOrganization(in PluginOrganization) error {
	if in.Folders == nil {
		return bad("INVALID_PLUGIN_FOLDER", "文件夹配置必须是对象")
	}
	seen := make(map[string]bool)
	for name, value := range in.Folders {
		if strings.TrimSpace(name) == "" || strings.ContainsFunc(name, unicode.IsControl) {
			return bad("INVALID_PLUGIN_FOLDER", "文件夹名称不能为空或包含控制字符")
		}
		members, err := pluginFolderMembers(value)
		if err != nil {
			return err
		}
		for _, member := range members {
			if !pluginStorageID.MatchString(member) {
				return bad("INVALID_PLUGIN_FOLDER", "文件夹包含无效插件编号")
			}
			if _, nested := in.Folders[member]; nested {
				return bad("INVALID_PLUGIN_FOLDER", "文件夹不能嵌套")
			}
			if seen[member] {
				return bad("INVALID_PLUGIN_FOLDER", "同一插件不能重复加入文件夹")
			}
			seen[member] = true
		}
	}
	var order []json.RawMessage
	if json.Unmarshal(in.Order, &order) != nil || bytes.Equal(bytes.TrimSpace(in.Order), []byte("null")) {
		return bad("INVALID_PLUGIN_ORDER", "插件顺序必须是列表")
	}
	seenOrder := make(map[string]bool)
	for _, value := range order {
		var id string
		if json.Unmarshal(value, &id) == nil {
			if !pluginStorageID.MatchString(id) {
				return bad("INVALID_PLUGIN_ORDER", "插件顺序包含无效编号")
			}
			id = "plugin:" + id
		} else {
			var item struct {
				ID    string          `json:"id"`
				Type  string          `json:"type"`
				Order json.RawMessage `json:"order"`
			}
			if json.Unmarshal(value, &item) != nil {
				return bad("INVALID_PLUGIN_ORDER", "插件顺序项无效")
			}
			if item.Type == "" {
				item.Type = "plugin"
			}
			if item.Type != "plugin" && item.Type != "folder" {
				return bad("INVALID_PLUGIN_ORDER", "插件顺序项无效")
			}
			if len(item.Order) > 0 {
				var numeric float64
				if json.Unmarshal(item.Order, &numeric) != nil || bytes.Equal(bytes.TrimSpace(item.Order), []byte("null")) || math.IsInf(numeric, 0) || math.IsNaN(numeric) {
					return bad("INVALID_PLUGIN_ORDER", "插件顺序必须使用有限数值")
				}
			}
			if item.Type == "plugin" && !pluginStorageID.MatchString(item.ID) {
				return bad("INVALID_PLUGIN_ORDER", "插件顺序包含无效编号")
			}
			// Original PluginOrder keeps stale folder entries after deletion or
			// rename. They are inert display preferences, not a nested folder.
			if item.Type == "folder" && strings.TrimSpace(item.ID) == "" {
				return bad("INVALID_PLUGIN_ORDER", "插件顺序包含空文件夹名称")
			}
			id = item.Type + ":" + item.ID
		}
		if seenOrder[id] {
			return bad("INVALID_PLUGIN_ORDER", "插件顺序不能包含重复项")
		}
		seenOrder[id] = true
	}
	return nil
}

func (p *PluginStore) saveOrganizationTx(ctx context.Context, tx *sql.Tx, in PluginOrganization) (PluginOrganization, error) {
	if err := p.checkPluginFormWrite(ctx, "", pluginFormWriteLifecycle); err != nil {
		return PluginOrganization{}, err
	}
	if err := validatePluginOrganization(in); err != nil {
		return PluginOrganization{}, err
	}
	current, err := p.organization(ctx, tx)
	if err != nil {
		return PluginOrganization{}, err
	}
	if current.Revision != in.Revision {
		return PluginOrganization{}, bad("PLUGIN_REVISION_CONFLICT", "插件文件夹或顺序已在其他页面更改，请刷新后重试")
	}
	in.Revision++
	raw, err := pluginJSON(struct {
		PluginOrganization
		OrderPresent bool `json:"order_present"`
	}{in, in.orderPresent}, true)
	if err != nil {
		return PluginOrganization{}, err
	}
	sealed, err := p.seal("organization", "admin", "main", raw)
	if err != nil {
		return PluginOrganization{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO plugin_organization(owner_id,revision,sealed,updated_at) VALUES(1,?,?,?) ON CONFLICT(owner_id) DO UPDATE SET revision=excluded.revision,sealed=excluded.sealed,updated_at=excluded.updated_at`, in.Revision, sealed, now())
	return in, err
}

func (p *PluginStore) SaveOrganization(ctx context.Context, in PluginOrganization) (PluginOrganization, error) {
	if err := p.checkPluginFormWrite(ctx, "", pluginFormWriteLifecycle); err != nil {
		return PluginOrganization{}, err
	}
	in.orderPresent = true
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return PluginOrganization{}, err
	}
	defer tx.Rollback()
	out, err := p.saveOrganizationTx(ctx, tx, in)
	if err != nil {
		return PluginOrganization{}, err
	}
	return out, tx.Commit()
}

// Both regular uninstall and PluginUnInstall use this same transaction. Folder
// appearance/unknown fields and PluginOrder are retained, as in the original.
func (p *PluginStore) removeOrganizationMemberTx(ctx context.Context, tx *sql.Tx, instance string) error {
	if err := p.checkPluginFormWrite(ctx, "", pluginFormWriteLifecycle); err != nil {
		return err
	}
	organization, err := p.organization(ctx, tx)
	if err != nil {
		return err
	}
	changed := false
	for name, value := range organization.Folders {
		members, err := pluginFolderMembers(value)
		if err != nil {
			return err
		}
		remaining := make([]string, 0, len(members))
		removed := false
		for _, member := range members {
			if member == instance {
				removed = true
			} else {
				remaining = append(remaining, member)
			}
		}
		if !removed {
			continue
		}
		changed = true
		list, _ := json.Marshal(remaining)
		if bytes.TrimSpace(value)[0] == '[' {
			organization.Folders[name] = list
		} else {
			var object map[string]json.RawMessage
			if err := json.Unmarshal(value, &object); err != nil {
				return err
			}
			object["plugins"] = list
			organization.Folders[name], _ = json.Marshal(object)
		}
	}
	if changed {
		_, err = p.saveOrganizationTx(ctx, tx, organization)
	}
	return err
}

func (a *App) pluginOrganizationHTTP(w http.ResponseWriter, r *http.Request) (any, error) {
	if r.Method == "GET" {
		return a.pluginStore.Organization(r.Context())
	}
	if r.Method != "PUT" {
		return nil, &APIError{"NOT_FOUND", "接口不存在", 404}
	}
	var in PluginOrganization
	if err := decodePluginBody(w, r, &in); err != nil {
		return nil, err
	}
	return a.pluginStore.SaveOrganization(r.Context(), in)
}
