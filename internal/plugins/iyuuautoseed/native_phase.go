package iyuuautoseed

import (
	"errors"
	download "light-media/internal/plugins/iyuuautoseed/native"
)

var ErrNativeOwnerDenied = errors.New("IYUU native operation owner denied")
var ErrNativeCancelled = errors.New("IYUU native operation cancelled before dispatch")

type nativeStoppedSeal struct{ marker byte }

var nativeStoppedProof = &nativeStoppedSeal{1}

type nativeStopped struct {
	class string
	seal  *nativeStoppedSeal
}

func (e *nativeStopped) Error() string { return nativeStoppedError(e.class).Error() }
func (e *nativeStopped) Unwrap() error { return nativeStoppedError(e.class) }
func nativeStoppedError(class string) error {
	if class == "cancelled" {
		return ErrNativeCancelled
	}
	return ErrNativeOwnerDenied
}
func nativeStoppedClass(e error) string {
	v, ok := e.(*nativeStopped)
	if !ok || v == nil || v.seal != nativeStoppedProof || (v.class != "owner_denied" && v.class != "cancelled") {
		return ""
	}
	return v.class
}

// Called only with the exact error returned by a native public mutation. A
// daemon error name or response outcome is never proof of no dispatch.
func nativeMutationOutcome(err error) (string, error) {
	if err == nil {
		return "confirmed", nil
	}
	if !download.MutationNotDispatched(err) {
		return "unknown", err
	}
	class := download.MutationFailureClass(err)
	if class == "native" {
		return "rejected", nil
	}
	if class == "owner_denied" || class == "cancelled" {
		return "rejected", &nativeStopped{class, nativeStoppedProof}
	}
	return "unknown", err
}
