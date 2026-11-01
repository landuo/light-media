package plugins

import (
	"bufio"
	"context"
	"errors"
)

// These seams transfer exactly one bounded authenticated frame. They do not
// install themselves into the ordinary call loops or negotiate material/v1.
// Lifecycle/negotiation glue must own the sole reader/serial gate and give the
// concurrent path the actual call account, never an account chosen by a worker.
// Keeping activation separate makes this first component auditable without
// silently enabling unlimited ordinary traffic or touching the three consumers.

type materialWireStats struct {
	SentBytes, ReadBytes   int
	SentFrames, ReadFrames int
}

func materialAddCount(current, n int) (int, error) {
	maxInt := int(^uint(0) >> 1)
	if current < 0 || n < 0 || n > maxInt-current {
		return current, failure("material_count_overflow")
	}
	return current + n, nil
}

// readMaterialLine uses the ordinary JSON-RPC codec and the actual negotiated
// physical frame bound. It retains partial wire counts even on EOF/size/codec
// failure. No policy exemption is assigned before the caller checks its grant.
func readMaterialLine(reader *bufio.Reader, maxFrame int) (Frame, int, error) {
	var raw []byte
	n := 0
	for {
		part, err := reader.ReadSlice('\n')
		var countErr error
		n, countErr = materialAddCount(n, len(part))
		if countErr != nil {
			return Frame{}, n, countErr
		}
		if n > maxFrame {
			return Frame{}, n, failure("frame_limit")
		}
		raw = append(raw, part...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil {
			return Frame{}, n, failure("worker_io_failed")
		}
		break
	}
	if materialUniqueJSON(raw) != nil {
		return Frame{}, n, failure("invalid_material_frame")
	}
	var frame Frame
	if decodeTransportFrame(raw, &frame) != nil {
		return Frame{}, n, failure("invalid_material_frame")
	}
	return frame, n, nil
}

func materialEncode(frame Frame, maxFrame int) ([]byte, error) {
	raw, err := frame.MarshalJSON()
	if err != nil || len(raw)+1 > maxFrame {
		return nil, failure("frame_limit")
	}
	return append(raw, '\n'), nil
}

func (s *wireSession) materialRead() (Frame, int, error) {
	if s.ctx.Err() != nil || s.aborted.Load() {
		return Frame{}, 0, failure("execution_canceled")
	}
	frame, n, err := readMaterialLine(s.reader, s.maxFrame)
	var countErr error
	s.outputBytes, countErr = materialAddCount(s.outputBytes, n)
	if countErr != nil {
		return Frame{}, n, countErr
	}
	return frame, n, err
}

func (s *wireSession) materialWrite(ctx context.Context, frame Frame) error {
	if ctx == nil || ctx.Err() != nil || s.ctx.Err() != nil || s.aborted.Load() {
		return failure("execution_canceled")
	}
	raw, err := materialEncode(frame, s.maxFrame)
	if err != nil {
		return err
	}
	// Preflight representability before writing; partial writes still count.
	if _, err = materialAddCount(s.inputBytes, len(raw)); err != nil {
		return err
	}
	if _, err = materialAddCount(s.materialInputBytes, len(raw)); err != nil {
		return err
	}
	if _, err = materialAddCount(s.materialSentFrames, 1); err != nil {
		return err
	}
	n, writeErr := s.writer.Write(raw)
	if n < 0 || n > len(raw) {
		return failure("worker_io_failed")
	}
	s.inputBytes += n
	s.materialInputBytes += n
	if n > 0 {
		s.materialSentFrames++
	}
	if writeErr != nil || n != len(raw) {
		return failure("worker_io_failed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func (s *wireSession) materialAdmitRead(n int) error {
	next, err := materialAddCount(s.materialOutputBytes, n)
	if err != nil {
		return err
	}
	frames, err := materialAddCount(s.materialReadFrames, 1)
	if err != nil {
		return err
	}
	s.materialOutputBytes = next
	s.materialReadFrames = frames
	return nil
}

// materialHostExchange is a serial reader/dispatcher seam, not a second reader
// goroutine. The ordinary call loop will invoke it only in its own read slot.
func (s *wireSession) materialHostExchange(ctx context.Context, scope *materialHostScope) error {
	if ctx == nil || ctx.Err() != nil {
		return failure("execution_canceled")
	}
	if scope == nil || s.identity != scope.binding.Identity || s.role != "host" || s.maxFrame != scope.maxFrame {
		return failure("material_identity_mismatch")
	}
	if err := scope.check(ctx); err != nil {
		return err
	}
	frame, n, err := s.materialRead()
	if err != nil {
		return err
	}
	reply, err := scope.handle(ctx, frame)
	if err != nil {
		return err
	}
	if err = s.materialAdmitRead(n); err != nil {
		return err
	}
	if err = scope.check(ctx); err != nil {
		return err
	}
	return s.materialWrite(ctx, reply)
}

func (s *wireSession) materialPeerExchange(ctx context.Context, invocation string, request Frame) (materialResponse, error) {
	var zero materialResponse
	r, err := decodeMaterialRequest(request)
	if err != nil || s.role != "worker" || request.Identity != s.identity || r.Grant.Invocation != invocation {
		return zero, failure("material_identity_mismatch")
	}
	if err = s.materialWrite(ctx, request); err != nil {
		return zero, err
	}
	reply, n, err := s.materialRead()
	if err != nil {
		return zero, err
	}
	value, err := materialAcceptReply(request, reply)
	if err != nil {
		return zero, err
	}
	if err = s.materialAdmitRead(n); err != nil {
		return zero, err
	}
	if err = ctx.Err(); err != nil {
		return zero, err
	}
	return value, nil
}

// Runtime-only fast path for the private constructor-owned immutable request.
// The original Frame seam above still revalidates arbitrary supplied frames.
func (s *wireSession) materialPeerExchangePrepared(ctx context.Context, invocation string, request materialPreparedRequest) (materialResponse, error) {
	var zero materialResponse
	if s.role != "worker" || request.frame.Identity != s.identity || request.checked.Grant.Invocation != invocation {
		return zero, failure("material_identity_mismatch")
	}
	if err := s.materialWrite(ctx, request.frame); err != nil {
		return zero, err
	}
	reply, n, err := s.materialRead()
	if err != nil {
		return zero, err
	}
	value, err := materialAcceptPreparedReply(request, reply)
	if err != nil {
		return zero, err
	}
	if err = s.materialAdmitRead(n); err != nil {
		return zero, err
	}
	if err = ctx.Err(); err != nil {
		return zero, err
	}
	return value, nil
}

func (s *wireSession) materialStats() materialWireStats {
	return materialWireStats{s.materialInputBytes, s.materialOutputBytes, s.materialSentFrames, s.materialReadFrames}
}

// Concurrent reads are still done by the one multiplexer reader. It hands the
// already-bounded frame/count to materialConcurrentHostFrame. Only that exact
// call's trusted scope/account may admit it; no ordinary host callback is spent.
func (t *concurrentTransport) materialReadLine() (Frame, concurrentWireCount, error) {
	if t.ctx.Err() != nil || t.wire.aborted.Load() {
		return Frame{}, concurrentWireCount{}, failure("execution_canceled")
	}
	frame, n, err := readMaterialLine(t.wire.reader, t.wire.maxFrame)
	return frame, concurrentWireCount{bytes: n}, err
}

// A reservation protects representable total counters before physical IO,
// without holding account.mu across a duplex write (the reader needs that lock).
// It is not a byte quota: released capacity follows actual bytes, never a page
// lifetime or a new budget epoch.
type materialSendReservation struct {
	account *concurrentAccount
	bytes   int
	done    bool
}

func (a *concurrentAccount) reserveMaterialSend(n int) (*materialSendReservation, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if n < 1 {
		return nil, failure("invalid_material_frame")
	}
	actual, err := materialAddCount(a.sent.bytes, a.materialReservedBytes)
	if err != nil {
		return nil, err
	}
	if _, err = materialAddCount(actual, n); err != nil {
		return nil, err
	}
	material, err := materialAddCount(a.materialSentBytes, a.materialReservedBytes)
	if err != nil {
		return nil, err
	}
	if _, err = materialAddCount(material, n); err != nil {
		return nil, err
	}
	frames, err := materialAddCount(a.materialSentFrames, a.materialReservedFrames)
	if err != nil {
		return nil, err
	}
	if _, err = materialAddCount(frames, 1); err != nil {
		return nil, err
	}
	reserved, err := materialAddCount(a.materialReservedBytes, n)
	if err != nil {
		return nil, err
	}
	a.materialReservedBytes = reserved
	a.materialReservedFrames++
	return &materialSendReservation{account: a, bytes: n}, nil
}

func (r *materialSendReservation) finish(written int) error {
	a := r.account
	a.mu.Lock()
	defer a.mu.Unlock()
	if r.done || written < 0 || written > r.bytes {
		return failure("invalid_material_count")
	}
	r.done = true
	a.materialReservedBytes -= r.bytes
	a.materialReservedFrames--
	// These adds were reserved before IO. Ordinary sends include pending
	// reservations in their preflight, so no concurrent update can steal it.
	a.sent.bytes += written
	a.materialSentBytes += written
	if written > 0 {
		a.materialSentFrames++
	}
	return nil
}

func (a *concurrentAccount) materialChargeRead(n int, admitted bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	next, err := materialAddCount(a.read.bytes, n)
	if err != nil {
		return err
	}
	// Invalid/partial physical reads remain actual bytes, never exemptions.
	a.read.bytes = next
	if admitted {
		material, err := materialAddCount(a.materialReadBytes, n)
		if err != nil {
			return err
		}
		frames, err := materialAddCount(a.materialReadFrames, 1)
		if err != nil {
			return err
		}
		a.materialReadBytes = material
		a.materialReadFrames = frames
	}
	return nil
}

func (t *concurrentTransport) materialWrite(ctx context.Context, frame Frame, account *concurrentAccount) error {
	if account == nil || ctx == nil || ctx.Err() != nil {
		return failure("execution_canceled")
	}
	raw, err := materialEncode(frame, t.wire.maxFrame)
	if err != nil {
		return err
	}
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	// Reuse the existing serialized physical writer. It returns the actual
	// count even on short write; material frames are already <= maxFrame.
	reservation, err := account.reserveMaterialSend(len(raw))
	if err != nil {
		return err
	}
	count, writeErr := t.writeLine(raw[:len(raw)-1])
	if err = reservation.finish(count.bytes); err != nil {
		return err
	}
	if writeErr != nil {
		return writeErr
	}
	return ctx.Err()
}

func materialConcurrentHostFrame(ctx context.Context, t *concurrentTransport, scope *materialHostScope, account *concurrentAccount, frame Frame, count concurrentWireCount) error {
	if scope == nil || account == nil || t.wire.role != "host" || t.wire.identity != scope.binding.Identity || t.wire.maxFrame != scope.maxFrame || !account.materialBound(scope) {
		return failure("material_identity_mismatch")
	}
	reply, err := scope.handle(ctx, frame)
	if e := account.materialChargeRead(count.bytes, err == nil); e != nil {
		return e
	}
	if err != nil {
		return err
	}
	if err = scope.check(ctx); err != nil {
		return err
	}
	return t.materialWrite(ctx, reply, account)
}

func (a *concurrentAccount) materialStats() materialWireStats {
	a.mu.Lock()
	defer a.mu.Unlock()
	return materialWireStats{a.materialSentBytes, a.materialReadBytes, a.materialSentFrames, a.materialReadFrames}
}

func (a *concurrentAccount) bindMaterialScope(scope *materialHostScope) error {
	if scope == nil {
		return failure("invalid_material_scope")
	}
	if err := scope.check(scope.ctx); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.materialScope != nil && a.materialScope != scope {
		return failure("material_account_mismatch")
	}
	a.materialScope = scope
	return nil
}
func (a *concurrentAccount) materialBound(scope *materialHostScope) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.materialScope == scope
}
