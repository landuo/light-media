package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type ownedGenerationHTTP struct {
	order           *[]string
	shutdownErr     error
	closeErr        error
	shutdownContext context.Context
}

func (h *ownedGenerationHTTP) Shutdown(ctx context.Context) error {
	*h.order = append(*h.order, "http-drain")
	h.shutdownContext = ctx
	if _, ok := ctx.Deadline(); !ok {
		return errors.New("missing existing shutdown deadline")
	}
	return h.shutdownErr
}
func (h *ownedGenerationHTTP) Close() error {
	*h.order = append(*h.order, "http-close")
	return h.closeErr
}

type ownedGenerationApp struct {
	order *[]string
	err   error
}

func (a *ownedGenerationApp) Close() error { *a.order = append(*a.order, "app-close"); return a.err }

func TestServerGenerationCloseOrderAndFailureReceipt(t *testing.T) {
	order := []string{}
	drainErr, httpErr, appErr := errors.New("owned drain"), errors.New("owned http close"), errors.New("owned app close")
	h := &ownedGenerationHTTP{order: &order, shutdownErr: drainErr, closeErr: httpErr}
	a := &ownedGenerationApp{order: &order, err: appErr}
	err := shutdownServerGeneration(func() { order = append(order, "cancel") }, h, a)
	if !reflect.DeepEqual(order, []string{"cancel", "http-drain", "http-close", "app-close"}) {
		t.Fatal(order)
	}
	if !errors.Is(err, drainErr) || !errors.Is(err, httpErr) || !errors.Is(err, appErr) {
		t.Fatal(err)
	}
	if !errors.Is(h.shutdownContext.Err(), context.Canceled) {
		t.Fatal("shutdown context leaked")
	}
}
