package agent

import (
	"context"
	"encoding/json"
	"testing"
)

func TestSafetyHookDeniesPowerShellDestructiveCommands(t *testing.T) {
	hook := NewSafetyHook(`C:\\repo`)
	for _, command := range []string{
		`Remove-Item -Recurse -Force .git`,
		`Remove-Item -Recurse -Force $PWD`,
		`Format-Volume -DriveLetter C`,
		`Clear-Disk -Number 0 -RemoveData -Confirm:$false`,
	} {
		args, err := json.Marshal(map[string]string{"command": command})
		if err != nil {
			t.Fatal(err)
		}
		decision := hook.BeforeToolCall(context.Background(), ToolCall{
			Name:      "bash",
			Arguments: args,
		}, ToolDefinition{})
		if decision.Action != HookDeny {
			t.Errorf("%q: action = %q, want deny", command, decision.Action)
		}
	}
}
