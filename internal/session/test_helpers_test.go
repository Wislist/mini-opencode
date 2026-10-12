package session

import "testing"

func newTestStore(t testing.TB, dir string) *Store {
	t.Helper()
	store := NewStore(dir)
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return store
}
