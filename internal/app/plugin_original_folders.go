package app

import (
	"bytes"
	"context"
	"encoding/json"
)

// Original list.remove removes the first equal member and leaves other fields,
// duplicate IDs and PluginOrder alone. This is not the native editor validator.
func (a *App) removeOriginalPluginFromFolders(ctx context.Context, instance string) error {
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	org, err := a.pluginStore.organization(ctx, tx)
	if err != nil {
		return err
	}
	changed := false
	for name, raw := range org.Folders {
		value := bytes.TrimSpace(raw)
		if len(value) == 0 {
			continue
		}
		var object map[string]json.RawMessage
		var listRaw json.RawMessage
		if value[0] == '[' {
			listRaw = raw
		} else if value[0] == '{' {
			if json.Unmarshal(raw, &object) != nil {
				continue
			}
			listRaw = object["plugins"]
		} else {
			continue
		}
		if len(listRaw) == 0 {
			continue
		}
		var list []json.RawMessage
		if err = json.Unmarshal(listRaw, &list); err != nil {
			return err
		}
		for n, item := range list {
			var id string
			if json.Unmarshal(item, &id) == nil && id == instance {
				list = append(list[:n], list[n+1:]...)
				next, e := json.Marshal(list)
				if e != nil {
					return e
				}
				if object == nil {
					org.Folders[name] = next
				} else {
					object["plugins"] = next
					org.Folders[name], err = json.Marshal(object)
					if err != nil {
						return err
					}
				}
				changed = true
				break
			}
		}
	}
	if !changed {
		return tx.Commit()
	}
	org.Revision++
	raw, err := json.Marshal(struct {
		PluginOrganization
		OrderPresent bool `json:"order_present"`
	}{org, org.orderPresent})
	if err != nil {
		return err
	}
	sealed, err := a.pluginStore.seal("organization", "admin", "main", raw)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO plugin_organization(owner_id,revision,sealed,updated_at) VALUES(1,?,?,?) ON CONFLICT(owner_id) DO UPDATE SET revision=excluded.revision,sealed=excluded.sealed,updated_at=excluded.updated_at`, org.Revision, sealed, now())
	if err != nil {
		return err
	}
	return tx.Commit()
}
