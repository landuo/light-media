package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

const (
	InputCreateCapability = "input.create"
	InputGetCapability    = "input.get"
	InputPopCapability    = "input.pop"
	InputRemoveCapability = "input.remove"
)

// InputInteractionRequest mirrors create_or_replace. The host supplies plugin_id
// from the current invocation, including clones; a worker cannot redirect it.
// UserID/ChatID accept strings or JSON integers. Nil channel/source differ from "".
type InputInteractionRequest struct {
	UserID          any             `json:"user_id"`
	Channel         *string         `json:"channel"`
	Source          *string         `json:"source"`
	Username        *string         `json:"username"`
	ChatID          any             `json:"chat_id,omitempty"`
	PromptID        *string         `json:"prompt_id,omitempty"`
	PromptMessageID any             `json:"prompt_message_id,omitempty"`
	TimeoutSeconds  *int            `json:"timeout_seconds,omitempty"`
	Payload         json.RawMessage `json:"payload,omitempty"`
}

type PendingInputInteraction struct {
	RequestID       string          `json:"request_id"`
	UserID          string          `json:"user_id"`
	PluginID        string          `json:"plugin_id"`
	Channel         *string         `json:"channel"`
	Source          *string         `json:"source"`
	Username        *string         `json:"username"`
	ChatID          *string         `json:"chat_id"`
	PromptID        *string         `json:"prompt_id"`
	PromptMessageID *string         `json:"prompt_message_id"`
	Payload         json.RawMessage `json:"payload"`
	TimeoutSeconds  int             `json:"timeout_seconds"`
	CreatedAt       time.Time       `json:"created_at"`
}

type InputInteractionQuery struct {
	UserID  any     `json:"user_id"`
	Channel *string `json:"channel"`
	Source  *string `json:"source"`
	ChatID  any     `json:"chat_id,omitempty"`
}

func inputInteractionCall(ctx context.Context, host HostCaller, method string, input any) (*PendingInputInteraction, error) {
	if host == nil {
		return nil, errors.New("input interaction requires an active host caller")
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	raw, err = host(ctx, method, raw)
	if err != nil {
		return nil, err
	}
	var result *PendingInputInteraction
	err = json.Unmarshal(raw, &result)
	return result, err
}

func CreateInputInteraction(ctx context.Context, host HostCaller, input InputInteractionRequest) (*PendingInputInteraction, error) {
	return inputInteractionCall(ctx, host, InputCreateCapability, input)
}
func GetInputInteraction(ctx context.Context, host HostCaller, input InputInteractionQuery) (*PendingInputInteraction, error) {
	return inputInteractionCall(ctx, host, InputGetCapability, input)
}
func PopInputInteraction(ctx context.Context, host HostCaller, input InputInteractionQuery) (*PendingInputInteraction, error) {
	return inputInteractionCall(ctx, host, InputPopCapability, input)
}
func RemoveInputInteraction(ctx context.Context, host HostCaller, requestID string) error {
	_, err := inputInteractionCall(ctx, host, InputRemoveCapability, map[string]string{"request_id": requestID})
	return err
}
