// Package workspacetool provides local filesystem and shell tools for ds4go
// tool loops.
//
// The package is optional: callers choose the workspace root and explicitly opt
// into side-effecting capabilities such as file writes and shell commands. By
// default, paths are confined to the configured root, symlink traversal is
// rejected, writes are disabled, and shell execution is disabled. In-root file
// I/O is performed through an os.Root handle, so operations cannot escape the
// root even under concurrent symlink swaps.
//
// Routine tool failures (a missing file, a wrong edit anchor, a denied
// Confirm) are returned to the model as "ERROR: ..." tool results so a tool
// loop can continue; only context cancellation and errors returned by the
// Confirm callback abort the loop. Register methods only advertise tools the
// configuration permits.
//
// A typical read-only setup registers file reading, directory listing, and
// search:
//
//	w, err := workspacetool.New(workspacetool.Config{Root: "/abs/project"})
//	if err != nil {
//		return err
//	}
//	defer w.Close()
//
//	reg := ds4go.NewToolRegistry()
//	if err := w.RegisterReadOnly(reg); err != nil {
//		return err
//	}
//
// Editing tools require AllowWrite:
//
//	w, err := workspacetool.New(workspacetool.Config{
//		Root:       "/abs/project",
//		AllowWrite: true,
//	})
//
// Shell tools require AllowShell and run with the workspace root as their
// working directory. On Unix-like platforms, stopping a shell job also stops
// child processes created by that shell by default:
//
//	w, err := workspacetool.New(workspacetool.Config{
//		Root:       "/abs/project",
//		AllowShell: true,
//	})
package workspacetool
