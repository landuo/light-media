package mediaservervendors

import (
	"context"
	"encoding/json"
	"errors"
	"light-media/internal/plugins"
	"light-media/internal/plugins/mediaserverruntime"
	"strings"
	"sync"
)

// TrimeMedia retains actual Api/user/version/library references. Small state
// locks never span vendor IO; construction/reconnect is not an atomic swap.
// Source class-default values are represented independently from assignment.
type TrimeMedia struct {
	mu                                                                              sync.RWMutex
	dependencies                                                                    TrimeDependencies
	host, username, password, accessCode, playHost                                  *string
	syncLibraries                                                                   []string
	hostRaw, usernameRaw, passwordRaw, accessCodeRaw, playHostRaw, syncLibrariesRaw json.RawMessage
	user                                                                            *TrimeUser
	api                                                                             TrimeAPI
	version                                                                         *TrimeVersion
	libraries                                                                       []*TrimeMediaDB
	assigned                                                                        map[string]bool
}

var _ mediaserverruntime.AuthenticatedVendor = (*TrimeMedia)(nil)

func TrimeMediaFactory(deps TrimeDependencies) (mediaserverruntime.Factory, error) {
	if deps.NewAPI == nil || deps.Report == nil {
		return nil, errors.New("TrimeMedia requires concrete Api and diagnostic dependencies")
	}
	return func(ctx context.Context, c plugins.MediaServerConfig) (mediaserverruntime.Vendor, error) {
		if err := trimeContext(ctx); err != nil {
			return nil, err
		}
		args, err := trimeParseArguments(c)
		if err != nil {
			return nil, err
		}
		p := &TrimeMedia{dependencies: deps, syncLibraries: []string{}, libraries: []*TrimeMediaDB{}, assigned: map[string]bool{}}
		if args.incomplete {
			p.log("error", "configuration-incomplete")
			return p, nil
		}
		p.hostRaw, p.usernameRaw, p.passwordRaw, p.accessCodeRaw = constructorJSON(args.host), constructorJSON(args.username), constructorJSON(args.password), constructorJSON(args.accessCode)
		p.host, p.username, p.password, p.accessCode = constructorStringProjection(args.host), constructorStringProjection(args.username), constructorStringProjection(args.password), constructorStringProjection(args.accessCode)
		p.syncLibrariesRaw = constructorJSON(args.syncLibraries)
		p.syncLibraries = constructorStringMembers(args.syncLibraries)
		for _, name := range []string{"_host", "_username", "_password", "_access_code", "_sync_libraries"} {
			p.assigned[name] = true
		}
		ok, err := p.Reconnect(ctx)
		if err != nil {
			return nil, err
		}
		if !ok {
			p.log("error", "connection-unavailable")
			return p, nil
		}
		api, _, err := p.createAPIRaw(ctx, args.playHost, args.accessCode)
		if err != nil {
			return nil, err
		}
		if !trimeNil(api) {
			host, err := api.Host(ctx)
			if err != nil {
				return nil, err
			}
			p.mu.Lock()
			p.playHost = trimeString(host)
			p.playHostRaw = constructorStringJSON(host)
			p.assigned["_playhost"] = true
			p.mu.Unlock()
			if err = api.Close(ctx); err != nil {
				return nil, err
			}
		} else if ejTruth(args.playHost) {
			p.log("warning", "play-host-unavailable")
			p.mu.Lock()
			play, err := constructorTrimURL(args.playHost)
			if err != nil {
				p.mu.Unlock()
				return nil, err
			}
			p.playHost = trimeString(play)
			p.playHostRaw = constructorStringJSON(play)
			p.assigned["_playhost"] = true
			p.mu.Unlock()
		}
		return p, nil
	}, nil
}
func (p *TrimeMedia) log(level, stage string) {
	p.dependencies.Report(TrimeDiagnostic{Level: level, Stage: stage})
}
func (p *TrimeMedia) createAPI(ctx context.Context, host, access *string) (TrimeAPI, *TrimeVersion, error) {
	return p.createAPIRaw(ctx, constructorOptionalString(host), constructorOptionalString(access))
}
func (p *TrimeMedia) createAPIRaw(ctx context.Context, host, access json.RawMessage) (TrimeAPI, *TrimeVersion, error) {
	if err := trimeContext(ctx); err != nil {
		return nil, nil, err
	}
	if !ejTruth(host) {
		return nil, nil, nil
	}
	origin, err := constructorTrimURL(host)
	if err != nil {
		return nil, nil, err
	}
	addresses := []string{}
	if !strings.HasSuffix(origin, "/v") {
		addresses = append(addresses, origin+"/v")
	}
	addresses = append(addresses, origin)
	for _, address := range addresses {
		var api TrimeAPI
		var err error
		if p.dependencies.NewRawAPI != nil {
			api, err = p.dependencies.NewRawAPI(ctx, address, "16CCEB3D-AB42-077D-36A1-F355324E4237", constructorJSON(access))
		} else {
			text := constructorStringProjection(access)
			api, err = p.dependencies.NewAPI(ctx, address, "16CCEB3D-AB42-077D-36A1-F355324E4237", text)
			if err == nil && text == nil && string(constructorJSON(access)) != "null" && !trimeNil(api) {
				consumer, ok := api.(TrimeRawAccess)
				if !ok {
					return nil, nil, errors.New("Trime Api lacks raw constructor access-code support")
				}
				err = consumer.SetConstructorAccessCode(ctx, constructorJSON(access))
			}
		}
		if err != nil {
			return nil, nil, err
		}
		if trimeNil(api) {
			return nil, nil, errors.New("TrimeMedia Api constructor returned no object")
		}
		verified, err := api.VerifyAccessCode(ctx)
		if err != nil {
			return nil, nil, err
		}
		if verified {
			version, err := api.SystemVersion(ctx)
			if err != nil {
				return nil, nil, err
			}
			if version != nil {
				return api, version, nil
			}
		}
		if err = api.Close(ctx); err != nil {
			return nil, nil, err
		}
	}
	return nil, nil, nil
}
func (p *TrimeMedia) IsConfigured(ctx context.Context) (bool, error) {
	if err := trimeContext(ctx); err != nil {
		return false, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return ejTruth(constructorPrefer(p.hostRaw, p.host)) && ejTruth(constructorPrefer(p.usernameRaw, p.username)) && ejTruth(constructorPrefer(p.passwordRaw, p.password)), nil
}
func (p *TrimeMedia) currentAPI(ctx context.Context, attribute string) (TrimeAPI, error) {
	if err := trimeContext(ctx); err != nil {
		return nil, err
	}
	p.mu.RLock()
	api := p.api
	p.mu.RUnlock()
	if trimeNil(api) {
		return nil, trimeMissing(attribute)
	}
	return api, nil
}
func (p *TrimeMedia) IsAuthenticated(ctx context.Context) (bool, error) {
	configured, err := p.IsConfigured(ctx)
	if err != nil || !configured {
		return false, err
	}
	p.mu.RLock()
	api := p.api
	p.mu.RUnlock()
	if trimeNil(api) {
		return false, nil
	}
	token, err := api.Token(ctx)
	if err != nil || token == nil {
		return false, err
	}
	p.mu.RLock()
	user := p.user
	p.mu.RUnlock()
	return user != nil, nil
}
func (p *TrimeMedia) IsInactive(ctx context.Context) (bool, error) {
	auth, err := p.IsAuthenticated(ctx)
	if err != nil {
		return false, err
	}
	if !auth {
		return true, nil
	}
	api, err := p.currentAPI(ctx, "user_info")
	if err != nil {
		return false, err
	}
	user, err := api.UserInfo(ctx)
	if err != nil {
		return false, err
	}
	p.mu.Lock()
	p.user = user
	p.assigned["_userinfo"] = true
	p.mu.Unlock()
	return user == nil, nil
}
func (p *TrimeMedia) Reconnect(ctx context.Context) (bool, error) {
	configured, err := p.IsConfigured(ctx)
	if err != nil || !configured {
		return false, err
	}
	if err = p.Disconnect(ctx); err != nil {
		return false, err
	}
	p.mu.RLock()
	host, access := constructorPrefer(p.hostRaw, p.host), constructorPrefer(p.accessCodeRaw, p.accessCode)
	p.mu.RUnlock()
	api, version, err := p.createAPIRaw(ctx, host, access)
	if err != nil {
		return false, err
	}
	if trimeNil(api) {
		return false, nil
	}
	p.mu.Lock()
	p.api, p.version = api, version
	p.assigned["_api"], p.assigned["_version"] = true, true
	p.mu.Unlock()
	p.log("debug", "version")
	api, err = p.currentAPI(ctx, "login")
	if err != nil {
		return false, err
	}
	p.mu.RLock()
	username, password := constructorPrefer(p.usernameRaw, p.username), constructorPrefer(p.passwordRaw, p.password)
	p.mu.RUnlock()
	var token *string
	userText, passwordText := constructorStringProjection(username), constructorStringProjection(password)
	if userText != nil && passwordText != nil {
		token, err = api.Login(ctx, *userText, *passwordText)
	} else if consumer, ok := api.(TrimeRawLogin); ok {
		token, err = consumer.LoginJSON(ctx, constructorJSON(username), constructorJSON(password))
	} else {
		return false, errors.New("Trime Api lacks raw JSON login support")
	}
	if err != nil || token == nil {
		return false, err
	}
	api, err = p.currentAPI(ctx, "user_info")
	if err != nil {
		return false, err
	}
	user, err := api.UserInfo(ctx)
	if err != nil {
		return false, err
	}
	p.mu.Lock()
	p.user = user
	p.assigned["_userinfo"] = true
	p.mu.Unlock()
	if user == nil {
		return false, nil
	}
	p.log("debug", "login-success")
	if _, err = p.GetLibraries(ctx, false); err != nil {
		return false, err
	}
	return true, nil
}
func (p *TrimeMedia) Disconnect(ctx context.Context) error {
	if err := trimeContext(ctx); err != nil {
		return err
	}
	p.mu.RLock()
	api := p.api
	p.mu.RUnlock()
	if trimeNil(api) {
		return nil
	}
	if _, err := api.Logout(ctx); err != nil {
		return err
	}
	api, err := p.currentAPI(ctx, "close")
	if err != nil {
		return err
	}
	if err = api.Close(ctx); err != nil {
		return err
	}
	p.mu.Lock()
	p.api, p.user = nil, nil
	p.assigned["_api"], p.assigned["_userinfo"] = true, true
	p.mu.Unlock()
	p.log("debug", "disconnected")
	return nil
}
func (p *TrimeMedia) Close(ctx context.Context) error { return p.Disconnect(ctx) }
