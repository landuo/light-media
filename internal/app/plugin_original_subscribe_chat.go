package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const originalSubscribeChatTitle = "订阅管理"

func (a *App) openOriginalSubscribeInteraction(ctx context.Context, reply originalSlashReply, args string) error {
	session, err := a.pluginOriginalSubscribes.start(reply.user)
	if err != nil {
		return err
	}
	if args != "" {
		handled, err := a.originalSubscribeInteractionText(ctx, reply, session, args)
		if handled || err != nil {
			return err
		}
	}
	return a.renderOriginalSubscribeInteraction(ctx, reply, session.id, false)
}

func (a *App) originalSubscribeInteractionCallback(ctx context.Context, reply originalSlashReply, data string) (bool, error) {
	parts := strings.SplitN(data, ":", 4)
	if len(parts) < 3 || parts[0] != "subscribes" {
		return false, nil
	}
	action := parts[2]
	_, ok := a.pluginOriginalSubscribes.step(reply.user, parts[1], originalSubscribeSessionAction(action), 0)
	if !ok {
		return true, reply.post(ctx, a, "订阅交互已失效，请重新发送 /subscribes", true)
	}
	switch action {
	case "close":
		return true, reply.show(ctx, a, originalSubscribeChatTitle, "订阅交互已结束", nil, true)
	case "refresh", "metadata":
		if err := a.runOriginalSubscribeRefresh(ctx, reply, action); err != nil {
			return true, err
		}
	}
	return true, a.renderOriginalSubscribeInteraction(ctx, reply, parts[1], true)
}

func originalSubscribeSessionAction(action string) string {
	switch action {
	case "refresh-list", "metadata":
		return "refresh"
	case "close", "page-prev", "page-next", "search", "delete", "refresh":
		return action
	}
	return ""
}

func originalSubscribePendingIDs(action, text string) bool {
	if action != "search" && action != "delete" {
		return false
	}
	digits := originalSiteDigits.FindAllString(text, -1)
	if len(digits) == 0 {
		return false
	}
	rest := originalSiteDigits.ReplaceAllString(text, "")
	return strings.TrimFunc(rest, func(r rune) bool {
		return originalSiteSpace(r) || r == ',' || r == '，' || r == '、'
	}) == ""
}

func originalSubscribeTextControl(text string) string {
	switch strings.ToLower(text) {
	case "退出", "关闭", "q", "quit", "exit":
		return "close"
	case "取消", "cancel", "返回", "back", "刷新列表", "列表", "list":
		return "refresh-list"
	case "刷新", "refresh":
		return "refresh"
	case "元数据", "刷新元数据", "metadata":
		return "metadata"
	case "p", "prev", "上一页":
		return "page-prev"
	case "n", "next", "下一页":
		return "page-next"
	}
	return ""
}

func (a *App) originalSubscribeInteractionText(ctx context.Context, reply originalSlashReply, session originalSlashSession, text string) (bool, error) {
	if session.id == "" {
		return false, nil
	}
	text = originalIncomingStrip(text)
	if action := originalSubscribeTextControl(text); action != "" {
		if _, ok := a.pluginOriginalSubscribes.step(reply.user, session.id, originalSubscribeSessionAction(action), 0); !ok {
			return false, nil
		}
		switch action {
		case "close":
			return true, reply.post(ctx, a, "订阅交互已结束", false)
		case "refresh", "metadata":
			if err := a.runOriginalSubscribeRefresh(ctx, reply, action); err != nil {
				return true, err
			}
		}
		return true, a.renderOriginalSubscribeInteraction(ctx, reply, session.id, false)
	}

	action, args := session.awaiting, text
	if action != "" {
		if !a.pluginOriginalSubscribes.consume(reply.user, session) {
			return false, nil
		}
	} else {
		if _, ok := a.pluginOriginalSubscribes.step(reply.user, session.id, "", 0); !ok {
			return false, nil
		}
		parts := strings.FieldsFunc(text, originalSiteSpace)
		if len(parts) > 1 {
			switch strings.ToLower(parts[0]) {
			case "search", "搜索":
				action = "search"
			case "delete", "删除":
				action = "delete"
			}
			args = strings.Join(parts[1:], " ")
		}
	}
	var message string
	var err error
	switch action {
	case "search":
		message, err = a.searchOriginalChatSubscriptions(ctx, reply, args)
	case "delete":
		message, err = a.deleteOriginalChatSubscriptions(ctx, args)
	default:
		_, hint := originalSubscribeChatPrompt("")
		return true, reply.post(ctx, a, hint, true)
	}
	if err != nil {
		return true, err
	}
	if err := reply.post(ctx, a, message, true); err != nil {
		return true, err
	}
	return true, a.renderOriginalSubscribeInteraction(ctx, reply, session.id, false)
}

var originalChatSubscriptionStateLabels = map[string]string{"N": "新建", "R": "订阅中", "P": "待定", "S": "暂停"}

func originalChatSubscriptionState(s Subscription) string {
	if s.State == "paused" {
		return "S"
	}
	if s.SourceState != "" {
		return s.SourceState
	}
	if s.LastCheck != "" {
		return "R"
	}
	return "N"
}

func originalSubscribeChatPrompt(mode string) (string, string) {
	switch mode {
	case "search":
		return "当前操作：搜索订阅，请输入订阅 ID，多个 ID 用空格分隔，或输入 all 搜索全部。", "输入订阅 ID 或 all；输入 `取消` 返回列表，输入 `退出` 结束交互。"
	case "delete":
		return "当前操作：删除订阅，请输入订阅 ID，多个 ID 用空格分隔。", "输入一个或多个订阅 ID；输入 `取消` 返回列表，输入 `退出` 结束交互。"
	}
	return "", "可输入：`搜索 <id...|all>`、`删除 <id...>`、`刷新`、`刷新元数据`、`n`、`p`、`退出`。"
}

func (a *App) renderOriginalSubscribeInteraction(ctx context.Context, reply originalSlashReply, id string, edit bool) error {
	rows, err := a.Store.originalSubscriptionAliases(ctx)
	if err != nil {
		return err
	}
	const pageSize = 6
	pages := max(1, (len(rows)+pageSize-1)/pageSize)
	session, ok := a.pluginOriginalSubscribes.step(reply.user, id, "clamp", pages)
	if !ok {
		return nil
	}
	text := "当前没有任何订阅。\n\n输入 `退出` 结束交互。"
	if len(rows) > 0 {
		lines := []string{"| ID | 名称 | 类型 | 年份 | 季/进度 | 状态 |", "| --- | --- | --- | --- | --- | --- |"}
		escape := strings.NewReplacer("\n", "<br>", "|", "\\|")
		start := session.page * pageSize
		for _, row := range rows[start:min(start+pageSize, len(rows))] {
			s := row.Subscription
			state := originalChatSubscriptionState(s)
			if display := originalChatSubscriptionStateLabels[state]; display != "" {
				state = display
			}
			year := "-"
			if s.SourceYearText != nil && *s.SourceYearText != "" {
				year = *s.SourceYearText
			} else if s.Media.Year > 0 {
				year = strconv.Itoa(s.Media.Year)
			}
			kind, progress := "电影", "电影"
			if s.Media.MediaType == "tv" {
				kind = "电视剧"
				progress = fmt.Sprintf("第%d季", s.Season)
				if s.TotalEpisodes > 0 {
					lack := s.TotalEpisodes
					if s.LackEpisode != nil {
						lack = *s.LackEpisode
					}
					progress += fmt.Sprintf(" [%d/%d]", max(0, s.TotalEpisodes-lack), s.TotalEpisodes)
				}
			}
			lines = append(lines, fmt.Sprintf("| %d | %s | %s | %s | %s | %s |", row.Number, escape.Replace(s.Media.Title), kind, escape.Replace(year), progress, escape.Replace(state)))
		}
		prompt, hint := originalSubscribeChatPrompt(session.awaiting)
		sections := []string{strings.Join(lines, "\n"), fmt.Sprintf("第 %d/%d 页，共 %d 个订阅", session.page+1, pages, len(rows))}
		if prompt != "" {
			sections = append(sections, prompt)
		}
		text = strings.Join(append(sections, hint), "\n\n")
	}
	button := func(text, action string) originalMessageButton {
		return originalMessageButton{text, "subscribes:" + id + ":" + action}
	}
	var buttons [][]originalMessageButton
	var nav []originalMessageButton
	if session.page > 0 {
		nav = append(nav, button("⬅️ 上一页", "page-prev"))
	}
	if session.page+1 < pages {
		nav = append(nav, button("下一页 ➡️", "page-next"))
	}
	if len(nav) > 0 {
		buttons = append(buttons, nav)
	}
	buttons = append(buttons,
		[]originalMessageButton{button("搜索订阅", "search"), button("删除订阅", "delete"), button("刷新订阅", "refresh")},
		[]originalMessageButton{button("刷新元数据", "metadata"), button("刷新列表", "refresh-list"), button("关闭", "close")})
	return reply.show(ctx, a, originalSubscribeChatTitle, text, buttons, edit)
}

func (a *App) runOriginalSubscribeRefresh(ctx context.Context, reply originalSlashReply, action string) error {
	start, finish := "开始刷新订阅...", "订阅刷新执行完成"
	run := a.originalChatRefreshSubscriptions
	if action == "metadata" {
		start, finish = "开始刷新订阅元数据...", "订阅元数据刷新完成"
		run = a.originalChatRefreshSubscriptionMetadata
	}
	if err := reply.post(ctx, a, start, true); err != nil {
		return err
	}
	if err := run(ctx); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return reply.post(ctx, a, a.clean(err.Error()), true)
	}
	return reply.post(ctx, a, finish, true)
}

func (a *App) originalChatSubscriptionNumber(ctx context.Context, number string) (Subscription, error) {
	value, err := strconv.ParseInt(number, 10, 64)
	if err != nil {
		return Subscription{}, sql.ErrNoRows
	}
	return a.Store.originalSubscriptionByAlias(ctx, value)
}

func (a *App) deleteOriginalChatSubscriptions(ctx context.Context, input string) (string, error) {
	ids := originalSiteDigits.FindAllString(input, -1)
	if len(ids) == 0 {
		return "请输入至少一个有效的订阅 ID", nil
	}
	var deleted, missing []string
	for _, raw := range ids {
		number := originalSiteDecimal(raw)
		s, err := a.originalChatSubscriptionNumber(ctx, number)
		if err == nil {
			err = a.cancelSubscriptionWithEvent(ctx, s.ID, false)
		}
		if errors.Is(err, sql.ErrNoRows) {
			missing = append(missing, number)
			continue
		}
		if err != nil {
			return "", err
		}
		deleted = append(deleted, s.Media.Title)
	}
	return originalChatSubscriptionResult("已删除 %d 个订阅", deleted, missing), nil
}

func originalChatSubscriptionResult(format string, names, missing []string) string {
	if len(names) == 0 && len(missing) != 0 {
		return "未找到订阅：" + strings.Join(missing, ", ")
	}
	out := fmt.Sprintf(format, len(names))
	if len(names) != 0 {
		out += "：" + strings.Join(names, ", ")
	}
	if len(missing) != 0 {
		out += "；未找到：" + strings.Join(missing, ", ")
	}
	return out
}

func (a *App) searchOriginalChatSubscriptions(ctx context.Context, reply originalSlashReply, input string) (string, error) {
	switch strings.ToLower(originalIncomingStrip(input)) {
	case "", "all", "全部", "所有":
		if err := reply.post(ctx, a, "开始搜索所有订阅...", true); err != nil {
			return "", err
		}
		rows, err := a.Store.originalSubscriptionAliases(ctx)
		if err != nil {
			return "", err
		}
		var attempted, failed int
		for _, row := range rows {
			s := row.Subscription
			state := originalChatSubscriptionState(s)
			if s.State != "active" || state != "N" && state != "R" && state != "P" {
				continue
			}
			attempted++
			if err := a.runOriginalChatSubscriptionSearch(ctx, s, false); err != nil {
				if ctx.Err() != nil {
					return "", ctx.Err()
				}
				failed++
			}
		}
		if failed != 0 {
			return fmt.Sprintf("订阅搜索结束，%d 个订阅未完成", failed), nil
		}
		message := "所有订阅搜索完成！"
		if attempted == 0 {
			message = "没有找到订阅！"
		}
		a.originalChatSubscriptionSearchNotice(message)
		return "所有订阅搜索完成", nil
	}

	ids := originalSiteDigits.FindAllString(input, -1)
	if len(ids) == 0 {
		return "请输入订阅 ID，多个 ID 用空格分隔，或输入 all", nil
	}
	var searched, missing, failed []string
	for _, raw := range ids {
		number := originalSiteDecimal(raw)
		s, err := a.originalChatSubscriptionNumber(ctx, number)
		if errors.Is(err, sql.ErrNoRows) {
			missing = append(missing, number)
			continue
		}
		if err != nil {
			return "", err
		}
		name := s.Media.Title
		if err := reply.post(ctx, a, "开始搜索订阅【"+name+"】...", true); err != nil {
			return "", err
		}
		if err := a.runOriginalChatSubscriptionSearch(ctx, s, true); err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			failed = append(failed, name)
			continue
		}
		a.originalChatSubscriptionSearchNotice(name + " 搜索完成！")
		searched = append(searched, name)
	}
	message := originalChatSubscriptionResult("已完成 %d 个订阅搜索", searched, missing)
	if len(failed) != 0 {
		message += "；未完成：" + strings.Join(failed, ", ")
	}
	return message, nil
}

func (a *App) originalChatSubscriptionSearchNotice(message string) {
	title := "订阅搜索"
	a.systemMessages.put("system", &title, message, nil, time.Now())
}

func (a *App) runOriginalChatSubscriptionSearch(ctx context.Context, s Subscription, explicit bool) error {
	if created, err := time.Parse(time.RFC3339Nano, s.CreatedAt); err == nil && time.Since(created) < time.Minute {
		return nil // the pinned search leaves a minute for editing a newly created row
	}
	job, err := a.enqueueOriginalChatSubscriptionSearch(ctx, s, explicit)
	if err != nil {
		return err
	}
	state, err := a.awaitOriginalChatSubscriptionJob(ctx, job.ID)
	if err != nil {
		return err
	}
	var settlement error
	if state == "succeeded" {
		settlement = a.settleOriginalChatSubscriptionSearch(ctx, s, job.ID)
	}
	if s.State == "active" && (state == "succeeded" || state == "failed") {
		if err := a.markOriginalChatSubscriptionAttempt(ctx, s); err != nil {
			return err
		}
	}
	if state != "succeeded" {
		return bad("SUBSCRIPTION_SEARCH_INTERRUPTED", "订阅搜索任务未完成")
	}
	return settlement
}

func (a *App) enqueueOriginalChatSubscriptionSearch(ctx context.Context, s Subscription, explicit bool) (Job, error) {
	if !a.originalChatSubscriptionIngress(ctx) {
		return Job{}, bad("PLUGIN_CAPABILITY_DENIED", "手动订阅搜索需要已验证的 Lark 消息")
	}
	paused := s.State == "paused" && explicit
	payload := originalChatPausedSearchPayload{ID: s.ID, Revision: &s.Revision, Paused: paused, Manual: true}
	expected := "active"
	if paused {
		expected = "paused"
	}
	for attempt := 0; attempt < 3; attempt++ {
		if err := a.guardSubscriptionState(ctx, s.ID, s.Revision, expected); err != nil {
			return Job{}, err
		}
		job, err := a.enqueue(ctx, "subscription", payload, id("wf_"))
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.Code != "JOB_EXISTS" {
			return job, err
		}
		var activeID string
		var revision sql.NullInt64
		var latest sql.NullString
		var activePaused, activeManual bool
		err = a.Store.DB.QueryRowContext(ctx, `SELECT id,json_extract(payload,'$.revision'),json_extract(payload,'$.latest_batch'),
			COALESCE(json_extract(payload,'$._chat_paused_search'),0),COALESCE(json_type(payload,'$._chat_manual_search')='true',0)
			FROM jobs WHERE kind='subscription' AND json_extract(payload,'$.id')=?
			AND state IN ('queued','running','retry_wait','waiting_paused') ORDER BY created_at DESC,id DESC LIMIT 1`, s.ID).Scan(&activeID, &revision, &latest, &activePaused, &activeManual)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return Job{}, err
		}
		if activeManual && revision.Valid && revision.Int64 == int64(s.Revision) && latest.String == "" && paused == activePaused {
			return Job{ID: activeID}, nil
		}
		state, err := a.awaitOriginalChatSubscriptionJob(ctx, activeID)
		if err != nil {
			return Job{}, err
		}
		if state == "waiting_paused" {
			break
		}
	}
	return Job{}, bad("SUBSCRIPTION_SEARCH_INTERRUPTED", "已有不同的订阅任务，搜索未提交")
}

func (a *App) awaitOriginalChatSubscriptionJob(ctx context.Context, id string) (string, error) {
	wait, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		var state string
		if err := a.Store.DB.QueryRowContext(wait, `SELECT state FROM jobs WHERE id=?`, id).Scan(&state); err != nil {
			return "", err
		}
		switch state {
		case "succeeded", "failed", "cancelled", "waiting_paused", "needs_review":
			return state, nil
		}
		select {
		case <-wait.Done():
			return "", wait.Err()
		case <-ticker.C:
		}
	}
}

func (a *App) markOriginalChatSubscriptionAttempt(ctx context.Context, attempted Subscription) error {
	a.subMu.Lock()
	defer a.subMu.Unlock()
	var current Subscription
	if err := a.Store.Get(ctx, "subscription", attempted.ID, &current); errors.Is(err, sql.ErrNoRows) {
		return nil // The original completion path may have deleted the subscription.
	} else if err != nil {
		return err
	}
	if current.Revision != attempted.Revision || current.State != "active" || current.SourceState != "N" {
		return nil
	}
	current.SourceState = "R"
	return a.Store.Put(ctx, "subscription", current.ID, current)
}
