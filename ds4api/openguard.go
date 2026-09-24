package ds4api

import "sync"

// EngineOpenGuard is runtime policy run before every engine open. It may
// refuse the open by returning an error, and may return a release function
// that the engine holds for its lifetime: called exactly once when the
// engine is closed (or collected), and called immediately when the native
// open fails. A guard returning both a release and an error has the release
// called immediately: the open never happens, so nothing else would free
// what the guard acquired.
//
// ds4api itself installs no guard: the strict binding layer carries no model
// management policy. The module root installs the single-runner run-lock
// policy when it is imported.
type EngineOpenGuard func(opts EngineOptions) (release func(), err error)

var (
	engineOpenGuardMu sync.Mutex
	engineOpenGuard   EngineOpenGuard
)

// SetEngineOpenGuard installs guard as the process-wide engine open policy
// and returns the previously installed guard, nil when there was none.
// Passing nil removes the policy. There is one slot: a caller adding policy
// of its own must chain to the returned guard, or the installed policy (the
// module root's run-lock, for one) is silently replaced.
func SetEngineOpenGuard(guard EngineOpenGuard) (previous EngineOpenGuard) {
	engineOpenGuardMu.Lock()
	defer engineOpenGuardMu.Unlock()
	previous = engineOpenGuard
	engineOpenGuard = guard
	return previous
}

func currentEngineOpenGuard() EngineOpenGuard {
	engineOpenGuardMu.Lock()
	defer engineOpenGuardMu.Unlock()
	return engineOpenGuard
}
