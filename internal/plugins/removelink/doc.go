// Package removelink implements the verified RemoveLink 2.17 state machine and
// SDK request shape without installing a plugin or starting filesystem watches.
//
// Machine consumes explicit file events and delegates effects to Ops. A real
// host must supply trusted invocation binding, authorized roots/storage, entity
// checks and durable effect receipts before connecting it to user media. The
// fixture backend is test-only; this package has no production file or network
// storage adapter. Machine.Stop preserves source stop_service queue draining,
// while the short-lived SDK protocol stop intentionally has no business effect.
package removelink
