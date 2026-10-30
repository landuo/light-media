package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
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
	folderKeys   []string // Python dict insertion order, separate from user display order.
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
		OrderPresent *bool    `json:"order_present"`
		FolderKeys   []string `json:"folder_keys"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		return out, err
	}
	out = stored.PluginOrganization
	// All rows created before this field were written by the native complete
	// organization endpoint, which always explicitly saved Order.
	out.orderPresent = stored.OrderPresent == nil || *stored.OrderPresent
	out.folderKeys = stored.FolderKeys
	if out.folderKeys == nil {
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		out.folderKeys = originalFolderInputKeys(fields["folders"])
	}
	out.Revision = revision
	return out, nil
}

func (p *PluginStore) Organization(ctx context.Context) (PluginOrganization, error) {
	p.folders.operation.Lock()
	defer p.folders.operation.Unlock()
	out, err := p.organization(ctx, p.store.DB)
	if err == nil {
		out = p.withOriginalFolders(out)
	}
	return out, err
}

func pluginFolderRawMembers(raw json.RawMessage) ([]json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, bad("INVALID_PLUGIN_FOLDER", "文件夹内容无效")
	}
	var members []json.RawMessage
	if bytes.TrimSpace(raw)[0] == '[' {
		if err := json.Unmarshal(raw, &members); err != nil {
			return nil, bad("INVALID_PLUGIN_FOLDER", "文件夹成员必须是插件编号列表")
		}
	} else if bytes.TrimSpace(raw)[0] == '{' {
		var object map[string]json.RawMessage
		if json.Unmarshal(raw, &object) != nil || object == nil {
			return nil, bad("INVALID_PLUGIN_FOLDER", "文件夹必须使用原版列表或对象格式")
		}
		if value, exists := object["plugins"]; exists {
			if json.Unmarshal(value, &members) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return nil, bad("INVALID_PLUGIN_FOLDER", "文件夹成员必须是插件编号列表")
			}
		}
	} else if !json.Valid(raw) {
		return nil, bad("INVALID_PLUGIN_FOLDER", "文件夹包含无效 JSON")
	}
	return members, nil
}

func originalFolderMemberIs(raw json.RawMessage, id string) bool {
	var value string
	return json.Unmarshal(raw, &value) == nil && value == id
}

func pluginFolderMembers(raw json.RawMessage) ([]string, error) {
	values, err := pluginFolderRawMembers(raw)
	if err != nil {
		return nil, err
	}
	members := make([]string, 0, len(values))
	for _, value := range values {
		var id string
		if json.Unmarshal(value, &id) != nil {
			return nil, bad("INVALID_PLUGIN_FOLDER", "文件夹成员不是插件编号")
		}
		members = append(members, id)
	}
	return members, nil
}

func validatePluginOrganization(in PluginOrganization) error {
	if in.Folders == nil {
		return bad("INVALID_PLUGIN_FOLDER", "文件夹配置必须是对象")
	}
	// Both source endpoints store configuration JSON, not code/path authority.
	// In particular the original folders dict has no member/schema validation,
	// and the user setting accepts list/dict/bool/int/string/null values.
	for _, value := range in.Folders {
		if !json.Valid(value) {
			return bad("INVALID_PLUGIN_FOLDER", "文件夹配置包含无效 JSON")
		}
	}
	if !json.Valid(in.Order) {
		return bad("INVALID_PLUGIN_ORDER", "插件顺序包含无效 JSON")
	}

	return nil
}

func (p *PluginStore) saveOrganizationTx(ctx context.Context, tx *sql.Tx, in PluginOrganization) (PluginOrganization, error) {
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
	in.folderKeys = originalFolderKeys(in)
	raw, err := pluginJSON(struct {
		PluginOrganization
		OrderPresent bool     `json:"order_present"`
		FolderKeys   []string `json:"folder_keys"`
	}{in, in.orderPresent, in.folderKeys}, true)
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
	in.orderPresent = true
	p.folders.operation.Lock()
	defer p.folders.operation.Unlock()
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return PluginOrganization{}, err
	}
	defer tx.Rollback()
	out, err := p.saveOrganizationTx(ctx, tx, in)
	if err != nil {
		return PluginOrganization{}, err
	}
	if err = tx.Commit(); err != nil {
		return PluginOrganization{}, err
	}
	p.publishOriginalFolders(out, true)
	return out, nil
}

// Both regular uninstall and PluginUnInstall use this same transaction. Folder
// appearance/unknown fields and PluginOrder are retained, as in the original.
func (p *PluginStore) removeOrganizationMemberTx(ctx context.Context, tx *sql.Tx, instance string) error {
	organization, err := p.organization(ctx, tx)
	if err != nil {
		return err
	}
	changed := false
	for _, name := range originalFolderKeys(organization) {
		value := organization.Folders[name]
		members, err := pluginFolderRawMembers(value)
		if err != nil {
			return err
		}
		remaining := make([]json.RawMessage, 0, len(members))
		removed := false
		for _, member := range members {
			if originalFolderMemberIs(member, instance) && !removed {
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
		return originalOrganizationHTTPValue(a.pluginStore.Organization(r.Context()))
	}
	if r.Method != "PUT" {
		return nil, &APIError{"NOT_FOUND", "接口不存在", 404}
	}
	// The Vue manager consumes one application-envelope projection of the two
	// original values. Keep it on their existing store/transaction path. A
	// returned revision is useful for stale-read display protection but is not
	// an original write precondition. Partial saves touch only the supplied
	// source value, so editing folders does not overwrite a newer user order.
	var in struct {
		Revision int64           `json:"revision"`
		Folders  json.RawMessage `json:"folders"`
		Order    json.RawMessage `json:"order"`
	}
	if err := decodePluginBody(w, r, &in); err != nil {
		return nil, err
	}
	var folders map[string]json.RawMessage
	if len(in.Folders) != 0 && (json.Unmarshal(in.Folders, &folders) != nil || folders == nil) {
		return nil, bad("INVALID_PLUGIN_FOLDER", "文件夹配置必须是对象")
	}
	if len(in.Order) != 0 {
		var order []json.RawMessage
		if json.Unmarshal(in.Order, &order) != nil || order == nil {
			return nil, bad("INVALID_PLUGIN_ORDER", "插件顺序必须是列表")
		}
	}
	if err := a.pluginStore.mutateOrganizationFields(r.Context(), len(in.Folders) != 0, func(current *PluginOrganization) (bool, error) {
		if len(in.Folders) != 0 {
			current.Folders = folders
			current.folderKeys = originalFolderInputKeys(in.Folders)
		}
		if len(in.Order) != 0 {
			current.Order = append(json.RawMessage(nil), in.Order...)
			current.orderPresent = true
		}
		return len(in.Folders) != 0 || len(in.Order) != 0, nil
	}); err != nil {
		return nil, err
	}
	return originalOrganizationHTTPValue(a.pluginStore.Organization(r.Context()))
}
