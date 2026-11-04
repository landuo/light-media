package mediaservervendors

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
)

func (a *TrimeNativeAPI) VerifyAccessCode(ctx context.Context) (bool, error) {
	if e := a.check(ctx); e != nil {
		return false, e
	}
	a.mu.RLock()
	raw := constructorPrefer(a.accessCodeRaw, a.accessCode)
	a.mu.RUnlock()
	if !ejTruth(raw) {
		return true, nil
	}
	code, err := constructorNativeString(raw, "TypeError")
	if err != nil {
		return false, err
	}
	if code == "" {
		return true, nil
	}
	root := strings.TrimSuffix(a.host, "/v")
	response, e := a.requestUtils(ctx, "get", root+"/c/"+trimeAPIQuoteAccess(code), a.protocol.AccessHeaders, nil)
	if e != nil {
		return false, e
	}
	if response == nil {
		a.report("ERROR", "access-unavailable", "")
		return false, nil
	}
	if response.Status == 404 {
		a.report("ERROR", "access-rejected", "")
		return false, nil
	}
	if response.Status >= 400 {
		a.report("ERROR", "access-status", "")
		return false, nil
	}
	return true, nil
}
func (a *TrimeNativeAPI) SystemVersion(ctx context.Context) (*TrimeVersion, error) {
	result, e := a.request(ctx, "/sys/version", "", nil)
	if e != nil {
		return nil, e
	}
	if !result.success() || !trimeAPITruthy(result.data) {
		return nil, nil
	}
	object, e := trimeAPIObject(result.data)
	if e != nil {
		return nil, e
	}
	front, e := trimeAPIOptionalString(object["version"])
	if e != nil {
		return nil, e
	}
	back, e := trimeAPIOptionalString(object["mediasrvVersion"])
	if e != nil {
		return nil, e
	}
	value := &TrimeVersion{Frontend: front, Backend: back}
	if e = a.check(ctx); e != nil {
		return nil, e
	}
	a.mu.Lock()
	a.version = value
	a.mu.Unlock()
	return value, nil
}
func (a *TrimeNativeAPI) Login(ctx context.Context, username, password string) (*string, error) {
	return a.loginData(ctx, username, password)
}
func (a *TrimeNativeAPI) LoginJSON(ctx context.Context, username, password json.RawMessage) (*string, error) {
	if !json.Valid(username) || !json.Valid(password) {
		return nil, errors.New("invalid owned Trime login JSON")
	}
	return a.loginData(ctx, constructorJSON(username), constructorJSON(password))
}
func (a *TrimeNativeAPI) loginData(ctx context.Context, username, password any) (*string, error) {
	ok, e := a.VerifyAccessCode(ctx)
	if e != nil || !ok {
		return nil, e
	}
	result, e := a.request(ctx, "/login", "", trimeJSONObject{{"username", username}, {"password", password}, {"app_name", "trimemedia-web"}})
	if e != nil {
		return nil, e
	}
	if result.success() {
		object, e := trimeAPIObject(result.data)
		if e != nil {
			return nil, e
		}
		token, e := trimeAPIOptionalString(object["token"])
		if e != nil {
			return nil, e
		}
		if e = a.check(ctx); e != nil {
			return nil, e
		}
		a.mu.Lock()
		a.token = token
		a.mu.Unlock()
	}
	return a.Token(ctx)
}
func (a *TrimeNativeAPI) Logout(ctx context.Context) (bool, error) {
	token, e := a.Token(ctx)
	if e != nil {
		return false, e
	}
	if token == nil || *token == "" {
		return true, nil
	}
	result, e := a.request(ctx, "/user/logout", "post", nil)
	if e != nil {
		return false, e
	}
	if result.success() && trimeAPITruthy(result.data) {
		if e = a.check(ctx); e != nil {
			return false, e
		}
		a.mu.Lock()
		a.token = nil
		a.mu.Unlock()
		return true, nil
	}
	return false, nil
}
func (a *TrimeNativeAPI) UserInfo(ctx context.Context) (*TrimeUser, error) {
	result, e := a.request(ctx, "/user/info", "", nil)
	if e != nil {
		return nil, e
	}
	if !result.success() {
		return nil, nil
	}
	object, e := trimeAPIObject(result.data)
	if e != nil {
		return nil, e
	}
	// Dataclass permits arbitrary __dict__ fields; this finite concrete bridge
	// admits only its existing ordinary typed value domain, never discards extras.
	for k := range object {
		if k != "guid" && k != "username" && k != "is_admin" {
			return nil, &TrimeArgumentBoundary{Field: "Api User dynamic field"}
		}
	}
	user := &TrimeUser{}
	if v, ok := object["guid"]; ok {
		user.GUID, e = trimeAPIString(v)
		if e != nil {
			return nil, e
		}
	}
	if v, ok := object["username"]; ok {
		user.Username, e = trimeAPIString(v)
		if e != nil {
			return nil, e
		}
	}
	if v, ok := object["is_admin"]; ok {
		switch x := v.(type) {
		case int, int64, *big.Int:
		case json.Number:
			if strings.ContainsAny(string(x), ".eE") {
				return nil, &TrimeArgumentBoundary{Field: "Api User raw integer"}
			}
		default:
			return nil, &TrimeArgumentBoundary{Field: "Api User raw integer"}
		}
		n, e := trimeAPIInteger(v)
		if e != nil {
			return nil, e
		}
		if !n.IsInt64() {
			return nil, &TrimeArgumentBoundary{Field: "Api User is_admin integer"}
		}
		user.IsAdmin = n.Int64()
	}
	return user, nil
}
func (a *TrimeNativeAPI) MediaDBList(ctx context.Context) ([]*TrimeMediaDB, error) {
	return a.mediaDBList(ctx, false)
}
func (a *TrimeNativeAPI) AdminMediaDBList(ctx context.Context) ([]*TrimeMediaDB, error) {
	return a.mediaDBList(ctx, true)
}
func (a *TrimeNativeAPI) mediaDBList(ctx context.Context, admin bool) ([]*TrimeMediaDB, error) {
	path := "/mediadb/list"
	if admin {
		path = "/mdb/list"
	}
	result, e := a.request(ctx, path, "", nil)
	if e != nil {
		return nil, e
	}
	if !result.success() {
		return nil, nil
	}
	if !trimeAPITruthy(result.data) {
		return []*TrimeMediaDB{}, nil
	}
	list, ok := result.data.([]any)
	if !ok {
		return nil, &TrimeArgumentBoundary{Field: "Api media list"}
	}
	out := make([]*TrimeMediaDB, 0, len(list))
	for _, entry := range list {
		object, e := trimeAPIObject(entry)
		if e != nil {
			return nil, e
		}
		guid, e := trimeAPIString(object["guid"])
		if e != nil {
			return nil, e
		}
		category := TrimeOthers
		if name, ok := object["category"].(string); ok {
			switch TrimeCategory(name) {
			case TrimeMovie, TrimeTV, TrimeMix, TrimeOthers:
				category = TrimeCategory(name)
			}
		}
		nameKey := "title"
		if admin {
			nameKey = "name"
		}
		value, present := object[nameKey]
		if !present {
			value = ""
		}
		name, e := trimeAPIOptionalString(value)
		if e != nil {
			return nil, e
		}
		posterValue, present := object["posters"]
		if !present {
			posterValue = []any{}
		}
		posters, e := trimeAPIStrings(posterValue)
		if e != nil {
			return nil, e
		}
		if posterValue == nil {
			return nil, trimeAPISource("TypeError", "'NoneType' object is not iterable")
		}
		for i, path := range posters {
			if path == "" {
				return nil, &TrimeArgumentBoundary{Field: "nullable Api poster requires nullable value bridge"}
			}
			if !strings.HasPrefix(path, "/") {
				path = "/" + path
			}
			posters[i] = "/api/v1/sys/img" + path
		}
		var directories []string
		if admin {
			directories, e = trimeAPIStrings(object["dir_list"])
			if e != nil {
				return nil, e
			}
		}
		out = append(out, &TrimeMediaDB{GUID: guid, Category: category, Name: name, Posters: posters, Directories: directories})
	}
	return out, nil
}
func (a *TrimeNativeAPI) ItemCount(ctx context.Context, guid string, types []TrimeItemType) (*int64, error) {
	if types == nil {
		types = []TrimeItemType{TrimeMovieItem, TrimeTVItem}
	}
	result, e := a.request(ctx, "/item/list", "", trimeJSONObject{{"ancestor_guid", guid}, {"tags", trimeJSONObject{{"type", types}}}, {"exclude_grouped_video", 1}, {"page", 1}, {"page_size", 1}})
	if e != nil {
		return nil, e
	}
	if !result.success() {
		return nil, nil
	}
	if !trimeAPITruthy(result.data) {
		zero := int64(0)
		return &zero, nil
	}
	object, e := trimeAPIObject(result.data)
	if e != nil {
		return nil, e
	}
	value := object["total"]
	if value == nil {
		value = object["total_count"]
	}
	if value == nil {
		return nil, nil
	}
	n, e := trimeAPIInteger(value)
	if e != nil {
		return nil, e
	}
	if !n.IsInt64() {
		return nil, &TrimeArgumentBoundary{Field: "Api count integer value bridge"}
	}
	number := n.Int64()
	return &number, nil
}
func (a *TrimeNativeAPI) TaskRunning(ctx context.Context) (any, error) {
	result, e := a.request(ctx, "/task/running", "", nil)
	if e != nil {
		return nil, e
	}
	return result.success() && trimeAPITruthy(result.data), nil
}
func (a *TrimeNativeAPI) ScanAll(ctx context.Context) (*bool, error) {
	result, e := a.request(ctx, "/mdb/scanall", "post", nil)
	if e != nil {
		return nil, e
	}
	v := result.success() && trimeAPITruthy(result.data)
	return &v, nil
}
func (a *TrimeNativeAPI) Scan(ctx context.Context, media *TrimeMediaDB) (bool, error) {
	if media == nil {
		return false, errors.New("Trime scan requires actual media object")
	}
	result, e := a.request(ctx, "/mdb/scan/"+media.GUID, "", trimeJSONObject{})
	if e != nil {
		return false, e
	}
	return result.success() && trimeAPITruthy(result.data), nil
}
