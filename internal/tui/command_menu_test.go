package tui

import "testing"

func TestFilterCommandsEmptyReturnsAll(t *testing.T) {
	got := filterCommands("")
	if len(got) != len(commandList) {
		t.Fatalf("got %d, want %d", len(got), len(commandList))
	}
}

func TestFilterCommandsByPrefix(t *testing.T) {
	got := filterCommands("/s")
	want := []string{"/status", "/session"}
	if len(got) != len(want) {
		t.Fatalf("got %d commands, want %d", len(got), len(want))
	}
	for i, c := range got {
		if c.Name != want[i] {
			t.Errorf("got %q, want %q", c.Name, want[i])
		}
	}
}

func TestFilterCommandsNoMatch(t *testing.T) {
	got := filterCommands("/xyz")
	if len(got) != 0 {
		t.Fatalf("got %d, want 0", len(got))
	}
}

func TestSortedCommandListIsAlphabetical(t *testing.T) {
	got := sortedCommandList()
	for i := 1; i < len(got); i++ {
		if got[i-1].Name > got[i].Name {
			t.Fatalf("not sorted: %q > %q", got[i-1].Name, got[i].Name)
		}
	}
}
