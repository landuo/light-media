package autosignin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"light-media/builtinplugins/plugins/autosignin/pyregex"
	legacyanime "light-media/builtinplugins/plugins/autosignin/pyvalue"
)

var ErrAdapterNotReady = errors.New("site adapter not ready")

type RetryCompileError struct{ Err error }

func (e *RetryCompileError) Error() string { return e.Err.Error() }
func (e *RetryCompileError) Unwrap() error { return e.Err }

var ErrRetryPatternNotReady = errors.New("retry expression outside verified compatibility")

type SourceEvent struct{ Channel, User any }
type RunHooks struct {
	VisitScoped func(context.Context, Object, string, bool, int) (Result, error)

	Visit  func(context.Context, Object, string, bool) (Result, error)
	Emit   func(context.Context, string, Object) error
	Notify func(context.Context, Object) error
	Match  func(string, string) (bool, error)
}

// Retry uses the same Python Unicode tables and source-pattern bridge as the
// installed fixed adapters. Explicit bridge gaps remain errors, never false.
func VerifiedRetryMatch(pattern, text string) (bool, error) {
	compiled, err := pyregex.CompileAt(pattern, false, pyregex.RetryOrigin)
	if err != nil {
		return false, &RetryCompileError{Err: err}
	}
	return compiled.MatchString(text)
}
func sourceInteger(value any) (*big.Int, error) {
	if f, ok := value.(float64); ok {
		if math.IsNaN(f) {
			return nil, &ValueError{"ValueError", "cannot convert float NaN to integer"}
		}
		if math.IsInf(f, 0) {
			return nil, &ValueError{"OverflowError", "cannot convert float infinity to integer"}
		}
	}
	if number, ok := value.(json.Number); ok && strings.ContainsAny(string(number), ".eE") {
		f, _ := strconv.ParseFloat(string(number), 64)
		if math.IsInf(f, 0) {
			return nil, &ValueError{"OverflowError", "cannot convert float infinity to integer"}
		}
	}
	switch n := value.(type) {
	case string:
		v, err := legacyanime.Integer(n)
		if err != nil {
			return nil, fmt.Errorf("invalid literal for int() with base 10: %s", sourceRepr(n))
		}
		return v, nil
	default:
		if value, ok := numeric(n); ok {
			return new(big.Int).Quo(value.Num(), value.Denom()), nil
		}
		return nil, fmt.Errorf("int() argument must be a string, a bytes-like object or a real number, not '%s'", typeName(n))
	}
}
func asObject(value any) Object { object, _ := sourceHTTPObject(value); return object }
func asList(value any) []any    { list, _ := value.([]any); return list }
func (e *Engine) history(key string) any {
	if e.HistoryLive != nil {
		value, err := e.HistoryLive(key)
		if err != nil {
			panic(&boundaryFailure{err})
		}
		return value
	}
	if v, exists := e.Data[key]; exists {
		return clone(v)
	}
	return nil
}
func (e *Engine) putHistory(key string, value any) error {
	if err := e.effect("history.put", key, value); err != nil {
		return err
	}
	e.Data[key] = clone(value)
	e.trace(Object{"op": "put", "key": key, "value": value})
	return nil
}
func (e *Engine) deleteHistory(key string) error {
	if err := e.effect("history.delete", key, nil); err != nil {
		return err
	}
	delete(e.Data, key)
	e.trace(Object{"op": "delete", "key": key})
	return nil
}

var sourceDecimalClass = legacyanime.DecimalClass()
var datedHistoryRE = regexp.MustCompile(`^(?:签到|登录)-` + sourceDecimalClass + `{4}-` + sourceDecimalClass + `{2}-` + sourceDecimalClass + `{2}$`)
var monthHistoryRE = regexp.MustCompile(`^` + sourceDecimalClass + `{1,2}月` + sourceDecimalClass + `{1,2}日$`)

func dateLabel(day time.Time) string { return fmt.Sprintf("%d月%d日", day.Month(), day.Day()) }

// datetime.strptime("%Y-%m-%d") uses ASCII month alternatives, but the
// second digit of days10..29 is Python Unicode decimal. CleanHistory's
// separate fullmatch still enforces the original4-2-2 key shape.
var sourceHistoryDate = regexp.MustCompile(`^(?:` + sourceDecimalClass + `{4})-(?:1[0-2]|0[1-9]|[1-9])-(?:3[0-1]|[1-2]` + sourceDecimalClass + `|0[1-9]|[1-9]| [1-9])$`)

func sourceDate(text string) (time.Time, bool) {
	if !sourceHistoryDate.MatchString(text) {
		return time.Time{}, false
	}
	parts := strings.Split(text, "-")
	if len(parts) != 3 {
		return time.Time{}, false
	}
	fields := [3]int{}
	for i, part := range parts {
		value, err := legacyanime.Integer(part)
		if err != nil || !value.IsInt64() {
			return time.Time{}, false
		}
		fields[i] = int(value.Int64())
	}
	if fields[0] < 1 || fields[0] > 9999 {
		return time.Time{}, false
	}
	date := time.Date(fields[0], time.Month(fields[1]), fields[2], 0, 0, 0, 0, time.UTC)
	return date, date.Year() == fields[0] && int(date.Month()) == fields[1] && date.Day() == fields[2]
}

// keys preserves the host storage iteration order. Maps are not substituted for
// the source's get_data() rows, so partial cleanup ordering remains observable.
func (e *Engine) CleanHistory(today time.Time, keys []string) error {
	first := today.AddDate(0, 0, -13)
	first = time.Date(first.Year(), first.Month(), first.Day(), 0, 0, 0, 0, time.UTC)
	retained := map[string]bool{}
	for i := 0; i < 14; i++ {
		retained[dateLabel(today.AddDate(0, 0, -i))] = true
	}
	for _, key := range keys {
		if datedHistoryRE.MatchString(key) {
			date, valid := sourceDate(strings.SplitN(key, "-", 2)[1])
			if valid && date.Before(first) {
				if err := e.deleteHistory(key); err != nil {
					return err
				}
			}
		} else if monthHistoryRE.MatchString(key) && !retained[key] {
			if err := e.deleteHistory(key); err != nil {
				return err
			}
		}
	}
	return nil
}
func (e *Engine) RemoveSite(selected, id any) (any, error) {
	all, err := contains(selected, "all")
	if err != nil {
		return nil, err
	}
	if all {
		return []any{"all"}, nil
	}
	if !Truth(selected) {
		return selected, nil
	}
	if text, ok := selected.(string); ok {
		selected = []any{text}
	}
	values, ok := selected.([]any)
	if !ok {
		return nil, fmt.Errorf("'%s' object is not iterable", typeName(selected))
	}
	retained := []any{}
	if Truth(id) {
		target, err := sourceInteger(id)
		if err != nil {
			return nil, err
		}
		for _, value := range values {
			candidate, err := sourceInteger(value)
			if err != nil {
				return nil, err
			}
			if candidate.Cmp(target) != 0 {
				retained = append(retained, value)
			}
		}
	}
	if len(retained) == 0 {
		e.setField("Enabled", false)
	}
	return retained, nil
}
func (e *Engine) SiteDeleted(id any) error {
	if !Truth(e.Config) {
		return nil
	}
	sign, login := e.Config["sign_sites"], e.Config["login_sites"]
	if !Truth(sign) {
		sign = []any{}
	}
	if !Truth(login) {
		login = []any{}
	}
	var err error
	sourceField6019_0, err := e.RemoveSite(sign, id)
	if err != nil {
		return err
	}
	e.setField("SignSites", sourceField6019_0)
	sourceField6095_0, err := e.RemoveSite(login, id)
	if err != nil {
		return err
	}
	e.setField("LoginSites", sourceField6095_0)
	return e.replaceConfig()
}

func parallelVisits(ctx context.Context, sites []Object, workers int, operation string, schemas bool, visit func(context.Context, Object, string, bool) (Result, error), scoped func(context.Context, Object, string, bool, int) (Result, error)) ([]Result, error) {
	if visit == nil && scoped == nil {
		return nil, ErrAdapterNotReady
	}
	results := make([]Result, len(sites))
	chunkSize := (len(sites) + workers*4 - 1) / (workers * 4)
	if chunkSize < 1 {
		chunkSize = 1
	}
	jobs := make(chan [2]int)
	var group sync.WaitGroup
	var failureMu sync.Mutex
	var firstError error
	for w := 0; w < workers; w++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for chunk := range jobs {
				var err error
				for i := chunk[0]; i < chunk[1]; i++ {
					if ctx.Err() != nil {
						err = ctx.Err()
						break
					}
					if scoped != nil {
						results[i], err = scoped(ctx, sites[i], operation, schemas, i)
					} else {
						results[i], err = visit(ctx, sites[i], operation, schemas)
					}
					if err != nil {
						break
					}
				}
				if err != nil {
					failureMu.Lock()
					if firstError == nil {
						firstError = err
					}
					failureMu.Unlock()
				}
			}
		}()
	}
send:
	for begin := 0; begin < len(sites); begin += chunkSize {
		end := begin + chunkSize
		if end > len(sites) {
			end = len(sites)
		}
		select {
		case jobs <- [2]int{begin, end}:
		case <-ctx.Done():
			failureMu.Lock()
			if firstError == nil {
				firstError = ctx.Err()
			}
			failureMu.Unlock()
			break send
		}
	}
	close(jobs)
	group.Wait()
	return results, firstError
}

func (e *Engine) Do(ctx context.Context, today time.Time, operation string, selected any, event *SourceEvent, keys []string, hooks RunHooks) error {
	if err := e.CleanHistory(today, keys); err != nil {
		return err
	}
	todayText := today.Format("2006-01-02")
	previousValue := e.history(operation + "-" + todayText)
	previous := Object{}
	if Truth(previousValue) {
		var ok bool
		previous, ok = previousValue.(Object)
		if !ok {
			return fmt.Errorf("'%s' object has no attribute 'get'", typeName(previousValue))
		}
	}
	var resultMap any = previous["results"]
	if !Truth(resultMap) {
		resultMap = Object{}
	}
	builtin := []Object{}
	for _, site := range e.indexers() {
		if !Truth(site["public"]) {
			builtin = append(builtin, site)
		}
	}
	allSites := append(append([]Object{}, builtin...), e.custom()...)
	all, err := contains(selected, "all")
	if err != nil {
		return err
	}
	sites := []Object{}
	for _, site := range allSites {
		include := true
		if Truth(selected) && !all {
			include, err = contains(selected, site["id"])
			if err != nil {
				return err
			}
		}
		if include {
			sites = append(sites, site)
		}
	}
	selectedIDs := []any{}
	for _, site := range sites {
		selectedIDs = append(selectedIDs, site["id"])
	}
	if !Truth(previous) || Truth(e.field("Clean")) {
		e.log("INFO", fmt.Sprintf("今日 %s 未%s，开始%s已选站点", todayText, operation, operation))
		if Truth(e.field("Clean")) {
			e.setField("Clean", false)
		}
	} else {
		retry, done := previous["retry"], previous["do"]
		if !Truth(retry) {
			retry = []any{}
		}
		if !Truth(done) {
			done = []any{}
		}
		pending := []Object{}
		for _, site := range sites {
			did, err := contains(done, site["id"])
			if err != nil {
				return err
			}
			include := !did
			if !include {
				include, err = contains(retry, site["id"])
				if err != nil {
					return err
				}
			}
			if !include {
				result, err := sourceHistoryGet(resultMap, pythonScalar(site["id"]), Object{})
				if err != nil {
					return err
				}
				success, err := sourceHistoryGet(result, "success", nil)
				if err != nil {
					return err
				}
				if !Truth(success) {
					// The original evaluates the results.get expression again only
					// after the success branch has failed.
					result, err = sourceHistoryGet(resultMap, pythonScalar(site["id"]), Object{})
					if err != nil {
						return err
					}
					logged, err := sourceHistoryGet(result, "logged_in", nil)
					if err != nil {
						return err
					}
					include = !Truth(logged)
				}
			}
			if include {
				pending = append(pending, site)
			}
		}
		if len(pending) == 0 {
			e.log("INFO", fmt.Sprintf("今日 %s 已%s，无重新%s站点，本次任务结束", todayText, operation, operation))
			return nil
		}
		sites = pending
		e.log("INFO", fmt.Sprintf("今日 %s 已执行%s，重试未确认登录或命中关键词的站点", todayText, operation))
	}
	if len(sites) == 0 {
		e.log("INFO", fmt.Sprintf("没有需要%s的站点", operation))
		return nil
	}
	e.log("INFO", fmt.Sprintf("开始执行%s任务 ...", operation))
	queue, err := sourceInteger(e.field("QueueCount"))
	if err != nil {
		return err
	}
	workers := len(sites)
	if queue.Cmp(big.NewInt(int64(workers))) < 0 {
		if !queue.IsInt64() {
			return errors.New("Number of processes must be at least 1")
		}
		workers = int(queue.Int64())
	}
	if workers <= 0 {
		return errors.New("Number of processes must be at least 1")
	}
	e.trace(Object{"op": "pool", "workers": workers})
	visit := hooks.Visit
	if visit != nil {
		visit = func(ctx context.Context, site Object, operation string, _ bool) (Result, error) {
			return hooks.Visit(ctx, site, operation, e.schemaLoaded())
		}
	}
	scoped := hooks.VisitScoped
	if scoped != nil {
		scoped = func(ctx context.Context, site Object, operation string, _ bool, index int) (Result, error) {
			return hooks.VisitScoped(ctx, site, operation, e.schemaLoaded(), index)
		}
	}
	visitCtx := withCacheDumpEntry(ctx, sourceCachePool)
	status, err := parallelVisits(visitCtx, sites, workers, operation, false, visit, scoped)
	if err != nil {
		return err
	}
	e.log("INFO", fmt.Sprintf("站点%s任务完成！", operation))
	monthlyKey := dateLabel(e.Now())
	monthly := e.history(monthlyKey)
	details := []any{}
	if Truth(monthly) {
		if list, ok := monthly.([]any); ok {
			details = list
		} else {
			details = []any{monthly}
		}
	}
	for i, result := range status {
		details = append(details, Object{"site": result.SiteName, "status": result.Message, "site_id": sites[i]["id"], "type": operation, "success": result.Success, "logged_in": result.LoggedIn})
	}
	if err := e.putHistory(monthlyKey, details); err != nil {
		return err
	}
	retrySites := []any{}
	retryMessages, loginMessages, signMessages, alreadyMessages, simulationMessages, failedMessages := []Result{}, []Result{}, []Result{}, []Result{}, []Result{}, []Result{}
	match := hooks.Match
	if match == nil {
		match = VerifiedRetryMatch
	}
	emit := func(data Object) error {
		if hooks.Emit == nil {
			return errors.New("source event sink not ready")
		}
		return hooks.Emit(ctx, "EventType.PluginAction", data)
	}
	for i, result := range status {
		id := sites[i]["id"]
		if err := sourceHistorySet(resultMap, pythonScalar(id), Object{"success": result.Success, "message": result.Message, "logged_in": result.LoggedIn}); err != nil {
			return err
		}
		if !result.LoggedIn && strings.Contains(result.Message, "Cookie已失效") {
			isBuiltin := false
			for _, site := range builtin {
				if equal(site["id"], id) {
					isBuiltin = true
					break
				}
			}
			if isBuiltin {
				e.log("INFO", fmt.Sprintf("触发站点 %s 自动登录更新Cookie和Ua", pythonScalar(result.SiteName)))
				data := Object{"site_id": id, "action": "site_refresh"}
				if err := emit(data); err != nil {
					return err
				}
				e.trace(Object{"op": "event", "event": "EventType.PluginAction", "data": data})
			}
		}
		if !result.Completed() {
			retrySites = append(retrySites, id)
		}
		if Truth(e.field("RetryKeyword")) && Truth(id) {
			pattern, ok := e.field("RetryKeyword").(string)
			if !ok {
				return errors.New("first argument must be string or compiled pattern")
			}
			matched, err := match(pattern, result.Message)
			if err != nil {
				return err
			}
			if matched {
				e.log("DEBUG", fmt.Sprintf("站点 %s 命中重试关键词 %s", pythonScalar(result.SiteName), pythonScalar(e.field("RetryKeyword"))))
				has, _ := contains(retrySites, id)
				if !has {
					retrySites = append(retrySites, id)
				}
				retryMessages = append(retryMessages, result)
				continue
			}
		}
		switch {
		case !result.Completed():
			failedMessages = append(failedMessages, result)
		case !result.Success || strings.Contains(result.Message, "登录成功"):
			loginMessages = append(loginMessages, result)
		case strings.Contains(pythonScalar(result.SiteName), "仿真签到成功") || strings.Contains(result.Message, "仿真签到成功"):
			simulationMessages = append(simulationMessages, result)
		case strings.Contains(pythonScalar(result.SiteName), "签到成功") || strings.Contains(result.Message, "签到成功"):
			signMessages = append(signMessages, result)
		case strings.Contains(pythonScalar(result.SiteName), "已签到") || strings.Contains(result.Message, "已签到"):
			alreadyMessages = append(alreadyMessages, result)
		default:
			failedMessages = append(failedMessages, result)
		}
	}
	if !Truth(e.field("RetryKeyword")) {
		retrySites = selectedIDs
	}
	e.log("DEBUG", fmt.Sprintf("下次%s重试站点 %s", operation, pythonList(retrySites)))
	if err := e.putHistory(operation+"-"+todayText, Object{"do": selectedIDs, "retry": retrySites, "results": resultMap}); err != nil {
		return err
	}
	if Truth(e.field("AutoCF")) {
		threshold, err := sourceInteger(e.field("AutoCF"))
		if err != nil {
			return err
		}
		if threshold.Sign() > 0 && len(retryMessages) > 0 && threshold.Cmp(big.NewInt(int64(len(retryMessages)))) <= 0 {
			data := Object{"action": "cloudflare_speedtest"}
			if err := emit(data); err != nil {
				return err
			}
			e.trace(Object{"op": "event", "event": "EventType.PluginAction", "data": data})
		}
	}
	notify := func(message Object) error {
		if hooks.Notify == nil {
			return errors.New("source notification sink not ready")
		}
		if err := hooks.Notify(ctx, message); err != nil {
			return err
		}
		e.trace(mergeObject(Object{"op": "message"}, message))
		return nil
	}
	if Truth(e.field("Notify")) {
		ordered := append(append(append(append(append(append([]Result{}, loginMessages...), signMessages...), alreadyMessages...), simulationMessages...), failedMessages...), retryMessages...)
		messages := []string{}
		for _, result := range ordered {
			messages = append(messages, "【"+pythonScalar(result.SiteName)+"】"+result.Message)
		}
		success, logged, failed := 0, 0, 0
		for _, result := range status {
			if result.Success {
				success++
			}
			if result.LoggedIn && !result.Success {
				logged++
			}
			if !result.Completed() {
				failed++
			}
		}
		counts := fmt.Sprintf("确认登录成功: %d \n", success)
		if operation == "签到" {
			counts = fmt.Sprintf("确认签到成功: %d \n仅确认登录: %d \n", success, logged)
		}
		counts += fmt.Sprintf("登录或访问失败: %d \n", failed)
		text := fmt.Sprintf("全部%s数量: %d \n本次%s数量: %d \n下次%s数量: %d \n", operation, len(selectedIDs), operation, len(sites), operation, len(retrySites)) + counts + strings.Join(messages, "\n")
		if err := notify(Object{"title": "【站点自动" + operation + "】", "mtype": "NotificationType.SiteMessage", "text": text}); err != nil {
			return err
		}
	}
	if event != nil {
		if err := notify(Object{"channel": event.Channel, "title": "站点" + operation + "完成！", "userid": event.User}); err != nil {
			return err
		}
	}
	return e.replaceConfig()
}
func mergeObject(a, b Object) Object {
	out := Object{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}
