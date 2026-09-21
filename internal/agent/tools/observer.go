package tools

// FileObserver records file access for the active session. It backs two
// guarantees that would otherwise be unenforceable at the tool layer:
//
//   - read-before-write: an existing file must be read before it is
//     overwritten or edited, so the agent never rewrites content it has not
//     seen (the same rule the harness applies through its fs observation
//     policy);
//   - snapshots: the pre-modification content is stored so a session can
//     restore what an edit replaced.
//
// Both are best effort from the tool's point of view: an observer failure must
// not block a legitimate edit, so implementations are expected to swallow
// storage errors after recording them.
type FileObserver interface {
	// RecordRead marks path as read by the session.
	RecordRead(path string) error
	// HasRead reports whether the session already read path.
	HasRead(path string) bool
	// Snapshot stores the pre-modification content of path.
	Snapshot(path string, content []byte) error
}

// observeRead records a read when an observer is attached.
func observeRead(observer FileObserver, path string) {
	if observer == nil {
		return
	}
	_ = observer.RecordRead(path)
}

// observeSnapshot stores pre-modification content when an observer is attached.
func observeSnapshot(observer FileObserver, path string, content []byte) {
	if observer == nil {
		return
	}
	_ = observer.Snapshot(path, content)
}

// checkReadBeforeWrite rejects a modifying call on an existing file the session
// never read. New files and a nil observer are always allowed.
func checkReadBeforeWrite(options FileOptions, path string, exists bool) error {
	if options.Observer == nil || !options.RequireReadBeforeWrite || !exists {
		return nil
	}
	if options.Observer.HasRead(path) {
		return nil
	}
	return errReadBeforeWrite(path)
}

type readBeforeWriteError struct{ path string }

func (e readBeforeWriteError) Error() string {
	return "read before write: " + e.path +
		" already exists and has not been read in this session; read it first (or pass the whole file to write to replace it deliberately)"
}

func errReadBeforeWrite(path string) error { return readBeforeWriteError{path: path} }
