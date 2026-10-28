package app

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"sort"
	"sync"
	"time"
)

type originalAuthTicket struct {
	userID     int64
	providerID string
	metadata   json.RawMessage
	createdAt  time.Time
	order      uint64
}

type originalAuthRuntime struct {
	mu       sync.Mutex
	tickets  map[string]originalAuthTicket
	sequence uint64
}

// Source time.time() is an IEEE-754 wall-clock value. Preserve its rounding
// at modern Unix epochs rather than introducing nanosecond TTL/eviction edges.
func originalAuthWallSeconds(at time.Time) float64 {
	return float64(at.Unix()) + float64(at.Nanosecond())/1e9
}

func (s *originalAuthRuntime) cleanup(at time.Time) {
	for key, value := range s.tickets {
		if originalAuthWallSeconds(at)-originalAuthWallSeconds(value.createdAt) > 120 {
			delete(s.tickets, key)
		}
	}
	if len(s.tickets) <= 1024 {
		return
	}
	keys := make([]string, 0, len(s.tickets))
	for key := range s.tickets {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		x, y := s.tickets[keys[i]], s.tickets[keys[j]]
		xTime, yTime := originalAuthWallSeconds(x.createdAt), originalAuthWallSeconds(y.createdAt)
		if xTime == yTime {
			return x.order < y.order
		}
		return xTime < yTime
	})
	for _, key := range keys[:len(keys)-1024] {
		delete(s.tickets, key)
	}
}

func (s *originalAuthRuntime) create(userID int64, providerID string, metadata json.RawMessage, at time.Time) (string, error) {
	at = at.Round(0) // Source uses wall-clock time.time(), not monotonic age.
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	ticket := base64.RawURLEncoding.EncodeToString(random[:])
	if !originalBaseJSONTruthy(metadata) {
		metadata = json.RawMessage(`{}`)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tickets == nil {
		s.tickets = make(map[string]originalAuthTicket)
	}
	// Exact source order: cleanup BEFORE insertion can leave 1025 tickets.
	s.cleanup(at)
	s.sequence++
	s.tickets[ticket] = originalAuthTicket{userID, providerID, append(json.RawMessage(nil), metadata...), at, s.sequence}
	return ticket, nil
}

func (s *originalAuthRuntime) consume(ticket string, at time.Time) (originalAuthTicket, bool) {
	at = at.Round(0)
	if ticket == "" {
		return originalAuthTicket{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.tickets[ticket]
	delete(s.tickets, ticket)
	s.cleanup(at)
	return value, ok && originalAuthWallSeconds(at)-originalAuthWallSeconds(value.createdAt) <= 120
}

func (a *App) originalAuthExchange(w http.ResponseWriter, r *http.Request) (any, error) {
	var body map[string]json.RawMessage
	invalid := func() (any, error) { return nil, &APIError{"INVALID_AUTH_EXCHANGE", "认证票据请求无效", 422} }
	if err := decodeBody(w, r, &body); err != nil || body == nil {
		return invalid()
	}
	var input *string
	if json.Unmarshal(body["ticket"], &input) != nil || input == nil {
		return invalid()
	}
	ticket, ok := a.pluginOriginalAuth.consume(*input, time.Now())
	if !ok {
		return nil, &APIError{"AUTH_TICKET_INVALID", "认证票据无效或已过期", 401}
	}
	// The current core has one actual local user. No synthetic users, active
	// flags, permissions or JWT are fabricated by this plugin bridge.
	if !a.originalAuthUserExists(r.Context(), ticket.userID) {
		return nil, &APIError{"AUTH_USER_UNAVAILABLE", "用户不存在或已禁用", 403}
	}
	token := id("")
	if _, err := a.Store.DB.ExecContext(r.Context(), `INSERT INTO sessions VALUES(?,?)`, tokenHash(token), time.Now().Add(7*24*time.Hour).Unix()); err != nil {
		return nil, err
	}
	http.SetCookie(w, sessionCookie(r, token, 604800))
	a.log("INFO", "auth", "plugin_login", "管理员已通过插件认证登录", "", "")
	return map[string]bool{"authenticated": true}, nil
}

func (a *App) originalAuthUserExists(ctx context.Context, id int64) bool {
	if id != 1 {
		return false
	}
	var found int
	return a.Store.DB.QueryRowContext(ctx, `SELECT id FROM admin WHERE id=?`, id).Scan(&found) == nil && found == 1
}

func (a *App) originalAuthTicketResource(ctx context.Context, scope originalBaseCallScope, raw json.RawMessage) (json.RawMessage, error) {
	p := scope.object
	if p == nil || p.app != a || scope.phase == "stop" || scope.phase == "init" || scope.phase == "get_state" || scope.phase == "get_auth_providers" || scope.phase == "get_render_mode" || !originalObjectStillLoaded(p) {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "认证票据需要当前插件的业务调用")
	}
	var input struct {
		UserID     int64           `json:"user_id"`
		ProviderID string          `json:"provider_id"`
		Metadata   json.RawMessage `json:"metadata"`
	}
	if err := originalResourceDecode(raw, &input); err != nil {
		return nil, err
	}
	p.sessionMu.Lock()
	concurrent := p.session != nil && p.session.SupportsConcurrent()
	p.sessionMu.Unlock()
	if !concurrent {
		return nil, bad("PLUGIN_CONCURRENT_REQUIRED", "认证票据需要协商并发worker")
	}
	providers, err := a.originalObjectAuthProviders(ctx, p)
	if err != nil {
		return nil, err
	}
	matched := false
	for _, provider := range providers {
		var providerID string
		if json.Unmarshal(provider["id"], &providerID) == nil && providerID == input.ProviderID && originalBaseJSONTruthy(provider["enabled"]) {
			matched = true
			break
		}
	}
	if !matched {
		return nil, bad("PLUGIN_AUTH_PROVIDER_DENIED", "当前插件未声明启用的认证提供方")
	}
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	if !a.originalAuthUserExists(ctx, input.UserID) {
		return nil, &APIError{"AUTH_USER_UNAVAILABLE", "本地认证用户不存在", 403}
	}
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	a.pluginOriginalRunningMu.Lock()
	defer a.pluginOriginalRunningMu.Unlock()
	if !originalObjectStillLoaded(p) || p.stopping.Load() {
		return nil, bad("PLUGIN_NOT_LOADED", "认证调用对象已停止")
	}
	ticket, err := a.pluginOriginalAuth.create(input.UserID, input.ProviderID, input.Metadata, time.Now())
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]string{"ticket": ticket})
}
