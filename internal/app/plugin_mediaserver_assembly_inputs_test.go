package app

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// One finite new seam root. No vendor is faked or declared loaded by this test:
// lifecycle admission is checked directly; identity uses a real owned App DB.
func TestOriginalMediaServerAssemblyNativeInputsAndAuthority(t *testing.T) {
	t.Run("private_lifetime_and_fresh_plugin_authority", func(t *testing.T) {
		root, cancel := context.WithCancel(context.Background())
		defer cancel()
		h := &originalMediaServerAssembly{phase: "running", root: root, cancel: cancel}
		if err := h.current(context.Background()); err == nil {
			t.Fatal("unbound context admitted")
		}
		foreign := &originalMediaServerAssembly{}
		if err := h.current(foreign.lifecycleContext(context.Background(), false)); err == nil {
			t.Fatal("foreign lifecycle token admitted")
		}
		trusted := h.lifecycleContext(root, false)
		if err := h.current(trusted); err != nil {
			t.Fatal(err)
		}
		var calls atomic.Int32
		var revoked atomic.Bool
		denial := errors.New("owned fresh invocation denied")
		call := context.WithValue(context.Background(), originalMediaServerOperationKey{}, &originalMediaServerOperation{current: func(context.Context) error {
			calls.Add(1)
			if revoked.Load() {
				return denial
			}
			return nil
		}})
		if err := h.current(call); err != nil {
			t.Fatal(err)
		}
		revoked.Store(true)
		if err := h.current(call); !errors.Is(err, denial) {
			t.Fatal("fresh revocation lost", err)
		}
		if calls.Load() != 2 {
			t.Fatal("invocation authority cached", calls.Load())
		}
		if err := h.current(trusted); err != nil {
			t.Fatal("unrelated worker revocation canceled actual host lifecycle", err)
		}
		cancel()
		if err := h.current(trusted); !errors.Is(err, context.Canceled) {
			t.Fatal("actual App parent cancellation lost", err)
		}
		if err := h.current(call); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled App allowed retained plugin invocation", err)
		}
		h.mu.Lock()
		h.phase = "closing"
		h.mu.Unlock()
		cleanup := h.lifecycleContext(context.Background(), true)
		if err := h.current(cleanup); err != nil {
			t.Fatal("explicit owned cleanup admission", err)
		}
		h.mu.Lock()
		h.phase = "closed"
		h.mu.Unlock()
		if err := h.current(cleanup); err == nil {
			t.Fatal("finished cleanup token remained valid")
		}
	})
	t.Run("same_existing_sealed_superuser_carrier", func(t *testing.T) {
		a := newTestApp(t)
		// Real ordinary setup, no socket. The value is the unique persisted admin,
		// not a username passed by a later request or a worker callback.
		setup := callTest(t, a.Handler(), "POST", "/api/auth/setup", `{"username":"owned-native-admin","password":"owned-bootstrap-test-password"}`, nil, "")
		if setup.Code != 200 {
			t.Fatal("owned setup", setup.Code)
		}
		expect := func(want string) {
			t.Helper()
			raw, err := originalNativeMediaServerSuperuser(context.Background(), a)
			if err != nil {
				t.Fatal(err)
			}
			var got string
			if json.Unmarshal(raw, &got) != nil || got != want {
				t.Fatal("private carrier mismatch")
			}
		}
		expect("owned-native-admin")
		noticePreferenceTest(t, a, "superuser", "", "owned-explicit-source-admin")
		expect("owned-explicit-source-admin")
		noticePreferenceTest(t, a, "superuser", "", "")
		expect("") // Explicit empty is not a missing value/default admin.
		noticePreferenceTest(t, a, "superuser", "", map[string]any{"invalid": "owned-only"})
		if _, err := originalNativeMediaServerSuperuser(context.Background(), a); err == nil {
			t.Fatal("malformed private carrier was converted into identity")
		}
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := originalNativeMediaServerSuperuser(canceled, a); !errors.Is(err, context.Canceled) {
			t.Fatal("identity cancellation", err)
		}
	})
	t.Run("real_scalar_providers_not_fixed_samples", func(t *testing.T) {
		nonce, err := originalNativeTrimeNonce(context.Background())
		if err != nil || nonce < 100000 || nonce > 999999 {
			t.Fatal("native nonce domain", err)
		}
		before := time.Now()
		seconds, err := originalNativeTrimeSeconds(context.Background())
		after := time.Now()
		if err != nil || seconds < originalNativeTrimeClockSeconds(before) || seconds > originalNativeTrimeClockSeconds(after) {
			t.Fatal("native clock outside actual observation bracket", err)
		}
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := originalNativeTrimeNonce(canceled); !errors.Is(err, context.Canceled) {
			t.Fatal("nonce cancellation", err)
		}
		if _, err := originalNativeTrimeSeconds(canceled); !errors.Is(err, context.Canceled) {
			t.Fatal("clock cancellation", err)
		}
		if _, err := originalNativeTrimeProtocol(nil); err == nil {
			t.Fatal("unresolved default headers accepted")
		}
	})
}
