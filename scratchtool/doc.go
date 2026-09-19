// Package scratchtool provides a session-scoped key/value scratchpad tool
// for ds4go tool loops.
//
// A scratchpad is small, durable working memory for the model that is neither
// the chat log nor the user's project tree: a flat map of key → text blob,
// persisted one file per key under $DS4_DIR/scratch/<session>/. The model
// lists keys, reads a value (optionally head or tail bytes), replaces a
// value, appends to a value, and deletes a key.
//
// Keys are flat names of 1–64 characters from [a-z0-9._-]; there are no
// directories or nested paths. Limits (MaxKeys, MaxValueBytes, MaxTotalBytes)
// are enforced before every write. Routine failures are returned to the model
// as "ERROR: ..." tool results so a tool loop can continue; only context
// cancellation aborts the loop.
//
// Writes are atomic (temp file renamed over the destination) and a Store is
// safe for concurrent use within one process. Per-key file I/O is confined to
// the session directory through an os.Root handle, and reads reject symlinks
// and other non-regular files. Two processes sharing a session are not
// coordinated and can race each other's writes.
//
// A typical setup registers the scratch tools alongside other tools:
//
//	store, err := scratchtool.New(scratchtool.Config{Session: "mytask"})
//	if err != nil {
//		return err
//	}
//	defer store.Close()
//
//	reg := ds4go.NewToolRegistry()
//	if err := store.Register(reg); err != nil {
//		return err
//	}
//	system := scratchtool.SystemHint + "\n\n" + mySystemPrompt
//
// A read-only registration advertises only scratch_list and scratch_get:
//
//	store, err := scratchtool.New(scratchtool.Config{
//		Session:  "mytask",
//		ReadOnly: true,
//	})
//	if err != nil {
//		return err
//	}
//	defer store.Close()
//
//	if err := store.RegisterReadOnly(reg); err != nil {
//		return err
//	}
package scratchtool
