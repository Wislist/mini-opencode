package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wislist/mini-opencode/internal/session"
)

// undoFixture returns a store with one saved session plus the on-disk target
// file the snapshot will restore.
func undoFixture(t *testing.T) (*session.Store, string, string) {
	t.Helper()
	dir := t.TempDir()
	store := session.NewStore(dir)
	sess := store.Create("undo")
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	target := filepath.Join(dir, "demo.txt")
	if err := os.WriteFile(target, []byte("new content"), 0644); err != nil {
		t.Fatalf("seed file error = %v", err)
	}
	return store, sess.ID, target
}

func TestRestoreLatestSnapshotRejectsMissingSession(t *testing.T) {
	var out bytes.Buffer
	if err := restoreLatestSnapshot(&out, nil, "id"); err == nil {
		t.Fatal("nil store: expected error")
	} else if !strings.Contains(err.Error(), "no active session") {
		t.Fatalf("nil store error = %v", err)
	}

	store, _, _ := undoFixture(t)
	if err := restoreLatestSnapshot(&out, store, ""); err == nil {
		t.Fatal("empty session id: expected error")
	} else if !strings.Contains(err.Error(), "no active session") {
		t.Fatalf("empty session id error = %v", err)
	}
}

func TestRestoreLatestSnapshotWithoutSnapshotLeavesDiskUntouched(t *testing.T) {
	store, id, target := undoFixture(t)

	var out bytes.Buffer
	err := restoreLatestSnapshot(&out, store, id)
	if err == nil {
		t.Fatal("expected error for session with no snapshot")
	}
	if !strings.Contains(err.Error(), "no snapshot recorded") {
		t.Fatalf("error = %v", err)
	}

	got, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatalf("ReadFile() error = %v", readErr)
	}
	if string(got) != "new content" {
		t.Fatalf("file changed on failed restore: %q", got)
	}
}

func TestRestoreLatestSnapshotWritesNewestContentBack(t *testing.T) {
	store, id, target := undoFixture(t)

	if err := store.SnapshotFile(id, target, []byte("old content")); err != nil {
		t.Fatalf("SnapshotFile() error = %v", err)
	}

	var out bytes.Buffer
	if err := restoreLatestSnapshot(&out, store, id); err != nil {
		t.Fatalf("restoreLatestSnapshot() error = %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(got) != "old content" {
		t.Fatalf("restored content = %q, want %q", got, "old content")
	}
	if !strings.Contains(out.String(), "restored "+target) {
		t.Fatalf("output = %q", out.String())
	}
}

// A snapshot pointing at an unwritable location must surface the error rather
// than panic or silently report success.
func TestRestoreLatestSnapshotReportsUnwritableTarget(t *testing.T) {
	store, id, _ := undoFixture(t)
	missing := filepath.Join(t.TempDir(), "no-such-dir", "nested", "file.txt")

	if err := store.SnapshotFile(id, missing, []byte("data")); err != nil {
		t.Fatalf("SnapshotFile() error = %v", err)
	}

	var out bytes.Buffer
	if err := restoreLatestSnapshot(&out, store, id); err == nil {
		t.Fatal("expected write error for unwritable target")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("unwritable target unexpectedly exists: %v", err)
	}
}
