package ds4api

import "sync"

// libCallMu serializes every call into libds4.
//
// libds4's Metal backend keeps process-global state: the command queue
// (g_queue), the batch command buffer (g_batch_cb), and the shared compute
// encoder (g_batch_enc) are all file-scope statics in ds4_metal.m. There is
// exactly one of each per process, not one per engine or session.
//
// Two inferences in flight at once therefore encode into the same command
// buffer, and one goroutine attaches a command encoder to a buffer the other
// goroutine has already committed. Metal aborts the process with a fatal
// "_status < MTLCommandBufferStatusCommitted" assertion. The Go race detector
// cannot catch this because the conflicting state lives in C memory.
//
// Every Engine and Session method holds this mutex for the duration of its
// libds4 call, making libds4 access single-threaded across the whole process.
// That matches the hardware: the GPU runs one graph at a time regardless.
var libCallMu sync.Mutex
