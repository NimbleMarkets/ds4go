package ds4api

import (
	"os"
	"runtime"
	"testing"
)

// libds4 dups the descriptor handed to ds4_set_stderr_fd and owns the dup.
// The mock must do the same: wrapping the caller's descriptor directly lets
// Go's file finalizer close that descriptor number later, after the caller
// has closed it and the number has been reused by something else, such as
// the directory handle the testing package opens to remove a TempDir.
func TestMockStderrDoesNotCloseACallersReusedDescriptor(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	fd := int32(w.Fd())
	mockStderr.set(fd)
	mockStderr.write("hello\n")
	if err := w.Close(); err != nil { // the caller is done with its end
		t.Fatal(err)
	}
	// The lowest free descriptor number is the one just closed; this file
	// takes it over.
	reuse, err := os.CreateTemp(t.TempDir(), "reuse")
	if err != nil {
		t.Fatal(err)
	}
	defer reuse.Close()
	if int32(reuse.Fd()) != fd {
		t.Skipf("descriptor %d was not reused (got %d); cannot exercise the finalizer race", fd, reuse.Fd())
	}
	mockStderr.set(-1) // drops the mock's reference; a finalizer would now be free to run
	runtime.GC()
	runtime.GC()
	if _, err := reuse.WriteString("still mine"); err != nil {
		t.Fatalf("write to the reused descriptor failed: %v (the mock closed a descriptor it did not own)", err)
	}
}
