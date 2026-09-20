package ds4

import (
	"os"

	"github.com/NimbleMarkets/ds4go/ds4api"
	"github.com/NimbleMarkets/ds4go/internal/models"
)

// The single-runner policy is root runtime policy, not binding behavior:
// importing this package installs it for every engine opened through ds4api,
// including engines opened directly on a Library. ds4api itself stays a
// strict binding with no model-management state.
func init() {
	ds4api.SetEngineOpenGuard(engineRunLockGuard)
}

// engineRunLockGuard takes the model's run lock for the engine lifetime so a
// second process cannot open the same model file, and model management can
// name the holder. Inspect-only opens and engines without a model file are
// exempt, as before.
func engineRunLockGuard(opts ds4api.EngineOptions) (func(), error) {
	if opts.ModelPath == "" || opts.InspectOnly {
		return nil, nil
	}
	lockPath := opts.ModelPath + ".run.lock"
	lock, err := models.AcquireEngineRunLock(opts.ModelPath)
	if err != nil {
		return nil, err
	}
	return func() {
		lock.Close()
		os.Remove(lockPath)
	}, nil
}
