package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

type pluginOrganizationCompatResponse struct {
	Success     bool           `json:"success"`
	Message     *string        `json:"message"`
	MessageI18N *string        `json:"message_i18n"`
	Data        map[string]any `json:"data"`
}

func organizationCompatResponse(success bool, message string) pluginOrganizationCompatResponse {
	out := pluginOrganizationCompatResponse{Success: success, Data: map[string]any{}}
	if message != "" {
		out.Message, out.MessageI18N = &message, &message
	}
	return out
}

// Legacy endpoints own only their corresponding source value. Each mutation
// reads and saves in one transaction, so a folder edit cannot overwrite a
// concurrently saved PluginOrder or unrelated folder properties.
func (p *PluginStore) mutateOrganization(ctx context.Context, change func(*PluginOrganization) (bool, error)) error {
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current, err := p.organization(ctx, tx)
	if err != nil {
		return err
	}
	changed, err := change(&current)
	if err != nil || !changed {
		return err
	}
	if _, err = p.saveOrganizationTx(ctx, tx, current); err != nil {
		return err
	}
	return tx.Commit()
}

func organizationCompatBody(w http.ResponseWriter, r *http.Request) (json.RawMessage, bool) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 2<<20))
	if err != nil {
		status, detail := 400, "Invalid body"
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			status, detail = 413, "Body too large"
		}
		pluginHTTPError(w, &pluginHTTPFailure{status, detail})
		return nil, false
	}
	if !json.Valid(raw) {
		pluginHTTPError(w, &pluginHTTPFailure{422, "Invalid JSON body"})
		return nil, false
	}
	return bytes.TrimSpace(raw), true
}

func organizationCompatTypeError(w http.ResponseWriter, raw json.RawMessage, kind string) {
	var input any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	_ = d.Decode(&input)
	typeName, message := "dict_type", "Input should be a valid dictionary"
	if kind == "list" {
		typeName, message = "list_type", "Input should be a valid list"
	}
	pluginHTTPError(w, &pluginHTTPFailure{422, []map[string]any{{"type": typeName, "loc": []string{"body"}, "msg": message, "input": input}}})
}

// Management aliases precede plugin-defined endpoints because "folders" is a
// reserved platform route. They use this application's administrator session,
// never a plugin API key or the old MoviePilot session/token.
func (a *App) servePluginOrganizationCompatAPI(w http.ResponseWriter, r *http.Request) bool {
	var suffix string
	matched := false
	for _, version := range []string{"v1", "v2"} {
		base := "/api/" + version + "/"
		if r.URL.Path == base+"user/config/PluginOrder" {
			suffix, matched = "order", true
			break
		}
		prefix := base + "plugin/folders"
		if r.URL.Path == prefix || strings.HasPrefix(r.URL.Path, prefix+"/") {
			suffix, matched = strings.TrimPrefix(r.URL.Path, prefix), true
			break
		}
	}
	if !matched {
		return false
	}
	if !a.authenticated(r) {
		pluginHTTPError(w, &pluginHTTPFailure{401, "Not authenticated"})
		return true
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && (!sameRequestOrigin(r) || r.Header.Get("Sec-Fetch-Site") == "cross-site") {
		pluginHTTPError(w, &pluginHTTPFailure{403, "Origin rejected"})
		return true
	}
	if strings.ContainsAny(r.URL.Path, "\\\x00") || strings.Contains(strings.ToLower(r.URL.RawPath), "%2f") || strings.Contains(strings.ToLower(r.URL.RawPath), "%5c") {
		pluginHTTPError(w, &pluginHTTPFailure{400, "Invalid path"})
		return true
	}
	parts := strings.Split(strings.TrimPrefix(suffix, "/"), "/")
	root, order := suffix == "", suffix == "order"
	member := len(parts) == 2 && parts[0] != "" && parts[1] == "plugins"
	folder := !root && !order && len(parts) == 1 && parts[0] != ""
	allowed := ""
	switch {
	case root || order:
		if r.Method != "GET" && r.Method != "POST" {
			allowed = "GET"
		}
	case member:
		if r.Method != "PUT" {
			allowed = "PUT"
		}
	case folder:
		if r.Method != "POST" && r.Method != "DELETE" {
			allowed = "POST"
		}
	default:
		pluginHTTPError(w, &pluginHTTPFailure{404, "Not Found"})
		return true
	}
	if allowed != "" {
		w.Header().Set("Allow", allowed)
		pluginHTTPError(w, &pluginHTTPFailure{405, "Method Not Allowed"})
		return true
	}
	if r.Method == "GET" {
		current, err := a.pluginStore.Organization(r.Context())
		if err != nil {
			pluginHTTPError(w, &pluginHTTPFailure{503, "Plugin organization unavailable"})
			return true
		}
		if order {
			out := organizationCompatResponse(true, "")
			out.Data["value"] = nil
			if current.orderPresent {
				out.Data["value"] = current.Order
			}
			pluginHTTPJSON(w, 200, out)
		} else {
			pluginHTTPJSON(w, 200, originalFolderJSON(current))
		}
		return true
	}
	var raw json.RawMessage
	if order && r.ContentLength == 0 {
		raw = json.RawMessage(`null`)
	} else if root || order || member {
		var ok bool
		raw, ok = organizationCompatBody(w, r)
		if !ok {
			return true
		}
	}
	if order && len(raw) > 0 && (raw[0] == '-' || raw[0] >= '0' && raw[0] <= '9') && bytes.ContainsAny(raw, ".eE") {
		integer, err := originalRatingInteger(raw)
		if err != nil {
			pluginHTTPError(w, &pluginHTTPFailure{422, "Input should be a valid integer"})
			return true
		}
		// In the source union, a JSON float matches lax bool before lax int;
		// strict JSON integers keep the int branch instead.
		if integer == "0" {
			raw = json.RawMessage(`false`)
		} else if integer == "1" {
			raw = json.RawMessage(`true`)
		} else {
			raw = json.RawMessage(integer)
		}
	}
	var folders map[string]json.RawMessage
	if root && (json.Unmarshal(raw, &folders) != nil || folders == nil) {
		organizationCompatTypeError(w, raw, "dict")
		return true
	}
	if member {
		var list []json.RawMessage
		if json.Unmarshal(raw, &list) != nil || bytes.Equal(raw, []byte("null")) {
			organizationCompatTypeError(w, raw, "list")
			return true
		}
	}
	out := organizationCompatResponse(true, "")
	err := a.pluginStore.mutateOrganization(r.Context(), func(current *PluginOrganization) (bool, error) {
		switch {
		case root:
			current.Folders = folders
			current.folderKeys = originalFolderInputKeys(raw)
		case order:
			current.Order = append(json.RawMessage(nil), raw...)
			current.orderPresent = true
		case member:
			if _, exists := current.Folders[parts[0]]; !exists {
				current.folderKeys = append(originalFolderKeys(*current), parts[0])
			}
			// The original PUT replaces a new-format object with a legacy list;
			// POST folders is the full appearance-preserving edit endpoint.
			current.Folders[parts[0]] = append(json.RawMessage(nil), raw...)
			out = organizationCompatResponse(true, "文件夹 '"+parts[0]+"' 中的插件已更新")
		case r.Method == "POST":
			if _, exists := current.Folders[parts[0]]; exists {
				out = organizationCompatResponse(false, "文件夹 '"+parts[0]+"' 已存在")
				return false, nil
			}
			current.Folders[parts[0]] = json.RawMessage(`[]`)
			current.folderKeys = append(current.folderKeys, parts[0])
			out = organizationCompatResponse(true, "文件夹 '"+parts[0]+"' 创建成功")
		case r.Method == "DELETE":
			if _, exists := current.Folders[parts[0]]; !exists {
				out = organizationCompatResponse(false, "文件夹 '"+parts[0]+"' 不存在")
				return false, nil
			}
			delete(current.Folders, parts[0])
			out = organizationCompatResponse(true, "文件夹 '"+parts[0]+"' 删除成功")
		}
		return true, nil
	})
	if err != nil {
		var api *APIError
		if errors.As(err, &api) {
			pluginHTTPError(w, &pluginHTTPFailure{422, map[string]string{"code": api.Code, "message": api.Message}})
		} else {
			pluginHTTPError(w, &pluginHTTPFailure{503, "Plugin organization unavailable"})
		}
		return true
	}
	pluginHTTPJSON(w, 200, out)
	return true
}
