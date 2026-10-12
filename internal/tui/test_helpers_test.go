package tui

import (
	"testing"

	"github.com/wislist/mini-opencode/internal/session"
)

func setTestHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

func newTestSessionStore(t *testing.T, dir string) *session.Store {
	t.Helper()
	store := session.NewStore(dir)
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close session store: %v", err)
		}
	})
	return store
}
