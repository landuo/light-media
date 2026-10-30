package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

const callbackErrorDetailLimit = 8192

// CallbackErrorDetail returns untrusted callback data, not a public error string.
// Only authorized host consumers with their normal secret redactor may use it.
// Empty detail is a present failure, not a successful callback.
func CallbackErrorDetail(err error) (string, bool) {
	var sdk *Error
	if errors.As(err, &sdk) && sdk.Code == "plugin_operation_failed" && sdk.callbackDetail != nil {
		return *sdk.callbackDetail, true
	}
	return "", false
}

func validCallbackErrorVersion(raw json.RawMessage) bool {
	var version struct {
		Version int `json:"version"`
	}
	return strictJSON(raw, &version) == nil && version.Version == 1
}

func (s *wireSession) negotiateCallbackErrors(advertised int) error {
	if advertised == 0 { // Old workers retain the code-only contract.
		return nil
	}
	if advertised != 1 {
		return failure("callback_error_mismatch")
	}
	raw, err := s.exchange("transport.callback_errors", json.RawMessage(`{"version":1}`))
	if err != nil {
		return err
	}
	if !validCallbackErrorVersion(raw) {
		return failure("callback_error_mismatch")
	}
	s.callbackErrors = true
	return nil
}

func validCallbackErrorDetail(kind, code string, detail *string) bool {
	return detail == nil || kind == "result" && code == "OPERATION_FAILED" && len(*detail) <= callbackErrorDetailLimit && utf8.ValidString(*detail)
}

func workerCallbackFailure(err error, negotiated bool) *ReplyError {
	reply := &ReplyError{Code: workerHandlerFailureCode(err)}
	var transport *Error
	if !negotiated || reply.Code != "OPERATION_FAILED" || errors.As(err, &transport) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return reply
	}
	detail := strings.ToValidUTF8(err.Error(), "�")
	if len(detail) > callbackErrorDetailLimit {
		// Do not truncate before host redaction: a partial configured secret
		// could evade its full-value match. Oversized errors stay code-only.
		return reply
	}
	reply.Detail = &detail
	return reply
}

// Validate negotiation before classifying a peer's reply as a reusable business
// failure. Protocol violations retire the entire session, including concurrent
// calls; they are not ordinary callback failures.
func validNegotiatedCallbackReply(reply *ReplyError, negotiated bool) bool {
	return reply == nil || reply.Detail == nil || negotiated && validCallbackErrorDetail("result", reply.Code, reply.Detail)
}

func workerReplyCallbackFailure(reply *ReplyError, negotiated bool) error {
	if reply.Detail == nil {
		return workerReplyFailure(reply.Code)
	}
	if !validNegotiatedCallbackReply(reply, negotiated) {
		return failure("invalid_frame")
	}
	detail := *reply.Detail
	return &Error{Code: "plugin_operation_failed", callbackDetail: &detail}
}
