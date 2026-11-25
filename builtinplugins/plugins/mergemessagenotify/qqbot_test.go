package mergemessagenotify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestQQBotOfficialProactiveNotification(t *testing.T) {
	var bot qqBot
	var requests []Request
	credentials := map[string]any{"app_id": " 123456 ", "app_secret": "fixture-secret", "target_openid": "u_123456"}
	get := func(key string) any { return credentials[key] }
	authCalls := 0
	http := func(_ context.Context, req Request) (Response, error) {
		requests = append(requests, req)
		if req.URL == "https://bots.qq.com/app/getAppAccessToken" {
			authCalls++
			return Response{Status: 200, JSON: map[string]any{"access_token": fmt.Sprintf("fixture-token-%d", authCalls), "expires_in": "7200"}}, nil
		}
		return Response{Status: 200, JSON: map[string]any{"id": "fixture-message-id"}}, nil
	}
	for i := 0; i < 2; i++ {
		if result, err := bot.send(t.Context(), http, get, Message{Title: "入库完成", Text: "测试影片"}); err != nil || result != true {
			t.Fatalf("unexpected result: %v, %v", result, err)
		}
	}
	credentials["app_secret"] = "fixture-rotated-secret"
	credentials["target_type"], credentials["target_openid"], credentials["sandbox"] = "group", "group_123456", true
	if result, err := bot.send(t.Context(), http, get, Message{Text: "群通知"}); err != nil || result != true {
		t.Fatalf("unexpected group result: %v, %v", result, err)
	}
	if authCalls != 2 || len(requests) != 5 {
		t.Fatalf("unexpected token/send count: %d/%d", authCalls, len(requests))
	}
	check := func(index int, endpoint string, body object, auth string) {
		t.Helper()
		req := requests[index]
		headers, _ := req.Kwargs["headers"].(map[string]any)
		if !strings.EqualFold(req.Method, "POST") || req.URL != endpoint || !reflect.DeepEqual(req.Kwargs["json"], body) {
			t.Fatalf("unexpected request contract at %d", index)
		}
		if auth == "" && len(headers) != 0 || auth != "" && headers["Authorization"] != auth {
			t.Fatalf("unexpected auth header at %d", index)
		}
	}
	check(0, "https://bots.qq.com/app/getAppAccessToken", obj("appId", "123456", "clientSecret", "fixture-secret"), "")
	for _, index := range []int{1, 2} {
		check(index, "https://api.sgroup.qq.com/v2/users/u_123456/messages", obj("content", "入库完成\n\n测试影片", "msg_type", 0), "QQBot fixture-token-1")
	}
	check(3, "https://bots.qq.com/app/getAppAccessToken", obj("appId", "123456", "clientSecret", "fixture-rotated-secret"), "")
	check(4, "https://sandbox.api.sgroup.qq.com/v2/groups/group_123456/messages", obj("content", "群通知", "msg_type", 0), "QQBot fixture-token-2")
}

func TestQQBotOfficialRejectionsDoNotReplayOrRevealSecrets(t *testing.T) {
	var bot qqBot
	values := map[string]any{"app_id": "123456", "app_secret": "fixture-secret", "target_openid": "fixture-openid"}
	get := func(key string) any { return values[key] }
	const private = "fixture-secret fixture-access-token fixture-openid"
	var authCalls, sendCalls int
	http := func(_ context.Context, req Request) (Response, error) {
		if req.URL == "https://bots.qq.com/app/getAppAccessToken" {
			authCalls++
			return Response{Status: 200, JSON: map[string]any{"access_token": "fixture-access-token", "expires_in": json.Number("7200")}}, nil
		}
		sendCalls++
		switch sendCalls {
		case 1:
			return Response{Status: 401, JSON: map[string]any{"err_code": json.Number("11255"), "message": private}}, nil
		case 2:
			return Response{Status: 403, JSON: map[string]any{"code": json.Number("40034105"), "message": private}}, nil
		case 3:
			return Response{Status: 200, JSON: map[string]any{"message_audit": map[string]any{"audit_id": private}}}, nil
		default:
			return Response{}, errors.New(private)
		}
	}
	for i, diagnostic := range []string{"11255", "40034105", "平台未返回有效确认", "未能确认发送结果"} {
		result, err := bot.send(t.Context(), http, get, Message{Title: "测试"})
		if result != false || err == nil || !strings.Contains(err.Error(), diagnostic) {
			t.Fatalf("rejection %d misclassified: %v, %v", i, result, err)
		}
		for _, value := range strings.Fields(private) {
			if strings.Contains(err.Error(), value) {
				t.Fatalf("private data leaked in error %d", i)
			}
		}
		if sendCalls != i+1 {
			t.Fatalf("notification %d was retried", i)
		}
	}
	if authCalls != 2 {
		t.Fatalf("expected one token invalidation after HTTP 401, got %d requests", authCalls)
	}
}

func TestQQBotRejectsUnsafeTargetBeforeAnyRequest(t *testing.T) {
	var bot qqBot
	get := func(key string) any {
		return map[string]any{"app_id": "123456", "app_secret": "fixture-secret", "target_openid": "../../../../another-path"}[key]
	}
	if result, err := bot.send(t.Context(), func(context.Context, Request) (Response, error) {
		t.Fatal("unsafe target reached the network")
		return Response{}, nil
	}, get, Message{Title: "测试"}); err == nil || result != false {
		t.Fatalf("unsafe target accepted: %v, %v", result, err)
	}
}
