// Package cleanuphost owns the continuous local RemoveLink 2.17 service.
//
// It supplies Linux inotify capture, serial lifecycle/timers, authorized local
// file identity checks, quarantine deletion and durable effect orchestration.
// It starts no service until a caller explicitly opens and configures a Host.
//
// App integration must supply a transactional Journal, idempotent core Effects
// and a trusted lifecycle/file-write Guard. No production SQL adapter, plugin
// registration, worker-run receipt or cloud storage adapter is supplied here.
// FileJournal and simulated Effects exist only in tests. SDK protocol stop is
// deliberately separate from source stop_service: WorkerStop does not drain.
//
// Native watches are Linux-only. Darwin tests use injected events and actual
// POSIX files; they do not claim a Darwin production observer implementation.
package cleanuphost
