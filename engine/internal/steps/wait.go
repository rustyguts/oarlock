package steps

import "context"

// --- wait.callback ---
//
// WaitCallback suspends its task until an external HTTP callback resumes it
// (design §4.1 "Long waits" — approvals). It returns a callback-kind Suspend
// immediately; the engine mints the resume token, persists the suspension, and
// merges the resume URL into the task output. No ResumeAt: only the callback
// (or a run cancel) ends the wait.

type WaitCallback struct{}

func (e *WaitCallback) Execute(_ context.Context, in TaskInput) (TaskOutput, error) {
	note, _ := in.Config["note"].(string)
	return TaskOutput{}, &Suspend{
		Kind:   "callback",
		Output: map[string]any{"waiting": true, "note": note},
	}
}
