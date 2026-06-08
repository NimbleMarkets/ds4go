package ds4api

import (
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

var (
	callbackMu        sync.Mutex
	callbackID        uintptr = 1
	tokenCallbacks            = map[uintptr]TokenEmitFunc{}
	doneCallbacks             = map[uintptr]GenerationDoneFunc{}
	progressCallbacks         = map[uintptr]ProgressFunc{}
	cancelCallbacks           = map[uintptr]CancelFunc{}
	abortCallbacks            = map[uintptr]AbortFunc{}

	tokenEmitCallback = purego.NewCallback(func(ud uintptr, token int32) {
		callbackMu.Lock()
		fn := tokenCallbacks[ud]
		callbackMu.Unlock()
		if fn != nil {
			fn(int(token))
		}
	})
	doneCallback = purego.NewCallback(func(ud uintptr) {
		callbackMu.Lock()
		fn := doneCallbacks[ud]
		callbackMu.Unlock()
		if fn != nil {
			fn()
		}
	})
	progressCallback = purego.NewCallback(func(ud uintptr, event unsafe.Pointer, current int32, total int32) {
		callbackMu.Lock()
		fn := progressCallbacks[ud]
		callbackMu.Unlock()
		if fn != nil {
			fn(goString(event), int(current), int(total))
		}
	})
	abortCallback = purego.NewCallback(func(ud uintptr, msg unsafe.Pointer) {
		invokeAbortCallback(ud, goString(msg))
	})
	cancelCallback = purego.NewCallback(func(ud uintptr) bool {
		return invokeCancelCallback(ud)
	})
)

func nextCallbackID() uintptr {
	callbackID++
	if callbackID == 0 {
		callbackID = 1
	}
	return callbackID
}

func registerTokenCallback(fn TokenEmitFunc) uintptr {
	if fn == nil {
		return 0
	}
	callbackMu.Lock()
	defer callbackMu.Unlock()
	id := nextCallbackID()
	tokenCallbacks[id] = fn
	return id
}

func unregisterTokenCallback(id uintptr) {
	if id == 0 {
		return
	}
	callbackMu.Lock()
	delete(tokenCallbacks, id)
	callbackMu.Unlock()
}

func registerDoneCallback(fn GenerationDoneFunc) uintptr {
	if fn == nil {
		return 0
	}
	callbackMu.Lock()
	defer callbackMu.Unlock()
	id := nextCallbackID()
	doneCallbacks[id] = fn
	return id
}

func unregisterDoneCallback(id uintptr) {
	if id == 0 {
		return
	}
	callbackMu.Lock()
	delete(doneCallbacks, id)
	callbackMu.Unlock()
}

func registerProgressCallback(fn ProgressFunc) uintptr {
	if fn == nil {
		return 0
	}
	callbackMu.Lock()
	defer callbackMu.Unlock()
	id := nextCallbackID()
	progressCallbacks[id] = fn
	return id
}

func unregisterProgressCallback(id uintptr) {
	if id == 0 {
		return
	}
	callbackMu.Lock()
	delete(progressCallbacks, id)
	callbackMu.Unlock()
}

func registerCancelCallback(fn CancelFunc) uintptr {
	if fn == nil {
		return 0
	}
	callbackMu.Lock()
	defer callbackMu.Unlock()
	id := nextCallbackID()
	cancelCallbacks[id] = fn
	return id
}

func unregisterCancelCallback(id uintptr) {
	if id == 0 {
		return
	}
	callbackMu.Lock()
	delete(cancelCallbacks, id)
	callbackMu.Unlock()
}

func invokeCancelCallback(id uintptr) bool {
	if id == 0 {
		return false
	}
	callbackMu.Lock()
	fn := cancelCallbacks[id]
	callbackMu.Unlock()
	return fn != nil && fn()
}

func registerAbortCallback(fn AbortFunc) uintptr {
	if fn == nil {
		return 0
	}
	callbackMu.Lock()
	defer callbackMu.Unlock()
	id := nextCallbackID()
	abortCallbacks[id] = fn
	return id
}

func unregisterAbortCallback(id uintptr) {
	if id == 0 {
		return
	}
	callbackMu.Lock()
	delete(abortCallbacks, id)
	callbackMu.Unlock()
}

func invokeAbortCallback(id uintptr, msg string) {
	if id == 0 {
		return
	}
	callbackMu.Lock()
	fn := abortCallbacks[id]
	callbackMu.Unlock()
	if fn != nil {
		fn(msg)
	}
}
