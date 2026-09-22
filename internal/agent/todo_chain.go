package agent

import (
	"fmt"
	"strings"
)

// TodoReader supplies the current task list so the run loop can drive a task
// list to completion without the user prompting after every item.
type TodoReader interface {
	// Load returns the stored todo list as JSON.
	Load() string
}

// TodoChainGate lets a hook suppress automatic continuation. Plan mode uses it:
// while plan mode blocks the work itself, pushing the agent to "continue" would
// only burn turns against a list it is not allowed to act on.
type TodoChainGate interface {
	BlocksTodoContinuation() bool
}

// continuationMarker opens every continuation message the runtime injects. It
// is exported so the UI can recognize those turns when a session is reloaded.
const continuationMarker = "[todo continuation"

// IsTodoContinuationText reports whether a user message was injected by the
// todo chain rather than typed by the user.
func IsTodoContinuationText(content string) bool {
	return strings.HasPrefix(strings.TrimSpace(content), continuationMarker)
}

// maxStalledContinuations is the dead-loop safety net for the todo chain, not a
// work budget: if the agent neither advances nor reports a blocker across this
// many consecutive continuation rounds, the run stops and says so. Whether work
// can continue is otherwise the agent's own judgement, expressed through
// todo_blocked.
const maxStalledContinuations = 3

// todoChain tracks one run's task-list progression.
type todoChain struct {
	reader TodoReader
	// lastFingerprint is the list state observed at the previous continuation.
	lastFingerprint string
	// stalled counts consecutive continuation rounds that changed nothing.
	stalled int
	// continuations counts how many times the run was pushed forward.
	continuations int
	// blocked is set when the agent reported a blocker through todo_blocked.
	blocked bool
	// gated is set when a hook (plan mode) forbids continuing.
	gated bool
}

// todoContinueStep is the decision for one end-of-turn check.
type todoContinueStep struct {
	// Continue reports whether the run should keep going.
	Continue bool
	// Instruction is the user message injected to push the work forward.
	Instruction string
	// Progress describes the outstanding list, for events and messaging.
	Progress TodoProgress
	// Reason explains why the chain stopped, when it did.
	Reason string
}

// next decides whether to continue the run for an outstanding task list.
//
// The agent owns the judgement: it keeps going while items remain, and it stops
// the chain by reporting a blocker through todo_blocked, or by finishing the
// list. The only thing the runtime enforces is a stall guard, so a model that
// neither advances nor reports a blocker cannot spin forever.
func (c *todoChain) next() todoContinueStep {
	if c.reader == nil || c.blocked || c.gated {
		return todoContinueStep{}
	}
	progress := SummarizeTodos(ParseTodos(c.reader.Load()))
	if !progress.Outstanding() {
		return todoContinueStep{Progress: progress}
	}

	if c.lastFingerprint != "" && progress.Fingerprint == c.lastFingerprint {
		c.stalled++
	} else {
		c.stalled = 0
	}
	c.lastFingerprint = progress.Fingerprint

	if c.stalled >= maxStalledContinuations {
		return todoContinueStep{
			Progress: progress,
			Reason: fmt.Sprintf(
				"the task list did not change across %d continuation rounds and the agent reported no blocker",
				c.stalled),
		}
	}

	c.continuations++
	return todoContinueStep{
		Continue:    true,
		Progress:    progress,
		Instruction: continuationInstruction(progress, c.stalled),
	}
}

// continuationInstruction builds the message that pushes the next task. The
// wording escalates with the stall count so a stuck model is pushed toward
// either acting or declaring a blocker, instead of silently repeating itself.
func continuationInstruction(progress TodoProgress, stalled int) string {
	var next string
	switch {
	case progress.InProgress != "":
		next = progress.InProgress
	case progress.Next != "":
		next = progress.Next
	default:
		next = "the next pending item"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "[todo continuation %d/%d] Outstanding task list:\n\n%s\n\n",
		progress.Completed, progress.Total, RenderTodos(progress.Items))
	fmt.Fprintf(&b, "Continue with: %s\n\n", next)

	switch stalled {
	case 0:
		b.WriteString("Do the work now, then call todo_write with the updated list: mark this item " +
			"completed and set the next one in_progress. Keep going through the list without waiting " +
			"for confirmation.")
	case 1:
		b.WriteString("The previous continuation round did not change the task list. Either make concrete " +
			"progress now (edit files, run commands, update the list) or, if something genuinely blocks " +
			"you, call todo_blocked with the reason and what you need.")
	default:
		b.WriteString("You have not advanced this list twice. Stop and report: call todo_blocked with " +
			"the exact blocker, or explain why the remaining items cannot be done. Do not repeat the " +
			"same reasoning again.")
	}
	return b.String()
}
