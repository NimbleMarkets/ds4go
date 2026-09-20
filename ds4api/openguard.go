package ds4api

import "sync"

// EngineOpenGuard is runtime policy run before every engine open. It may
// refuse the open by returning an error, and may return a release function
// that the engine holds for its lifetime: called exactly once when the
// engine is closed (or collected), and called immediately when the native
// open fails.
//
// ds4api itself installs no guard: the strict binding layer carries no model
// management policy. The module root installs the single-runner run-lock
// policy when it is imported.
type EngineOpenGuard func(opts EngineOptions) (release func(), err error)

var (
	engineOpenGuardMu sync.Mutex
	engineOpenGuard   EngineOpenGuard
)

// SetEngineOpenGuard installs guard as the process-wide engine open policy.
// Passing nil removes it.
func SetEngineOpenGuard(guard EngineOpenGuard) {
	engineOpenGuardMu.Lock()
	defer engineOpenGuardMu.Unlock()
	engineOpenGuard = guard
}

func currentEngineOpenGuard() EngineOpenGuard {
	engineOpenGuardMu.Lock()
	defer engineOpenGuardMu.Unlock()
	return engineOpenGuard
}
