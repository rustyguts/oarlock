package steps

import (
	"context"
	"time"
)

// Suspended is returned by an executor's Execute (or Resume) to tell the engine
// "I handed work off to something external; persist a checkpoint and free the
// worker slot." It is NOT a failure: the engine writes task status=suspended +
// a suspensions row instead of a terminal status, and re-invokes the executor's
// Resume later (on a schedule for poll/delay, or on a callback for callback).
//
// It is a sentinel error so it composes with errors.As (and survives wrapping
// with fmt.Errorf("%w")), and so the four synchronous native executors that
// never suspend are wholly unaffected — they simply never return it (hard rule
// 5: the suspension capability is additive).
type Suspended struct {
	// Kind is one of "poll", "callback", or "delay" (see the suspensions table
	// kind column). poll/delay schedule a resume at ResumeAt; callback waits for
	// POST /v1/resume/{token}.
	Kind string
	// ResumeAt is when the engine should re-invoke Resume (poll/delay). Zero
	// means callback-only: no scheduled resume is created.
	ResumeAt time.Time
	// Token is the callback resume token. Leave empty and the engine generates
	// one; read it back from the SuspensionState on the first Resume if you need
	// to hand it to the external system.
	Token string
	// Payload is an opaque, JSON-serializable checkpoint (e.g. a container
	// Handle). It round-trips through the suspensions.payload column and is
	// handed back to Resume. Never put raw secrets here — it is persisted.
	Payload map[string]any
}

func (s *Suspended) Error() string { return "step suspended: " + s.Kind }

// SuspendNow is the constructor executors use to suspend with a scheduled
// resume at the given time (poll/delay).
func SuspendNow(kind string, resumeAt time.Time, payload map[string]any) *Suspended {
	return &Suspended{Kind: kind, ResumeAt: resumeAt, Payload: payload}
}

// SuspendForCallback suspends with no scheduled resume; the step resumes only
// when POST /v1/resume/{token} is hit. The engine fills Token if empty.
func SuspendForCallback(payload map[string]any) *Suspended {
	return &Suspended{Kind: "callback", Payload: payload}
}

// Resumable is implemented by executors that can be re-invoked after a
// suspension. The engine resolves the executor from the registry by step type,
// type-asserts Resumable, and calls Resume with the rehydrated suspension. A
// step type that returns *Suspended but does not implement Resumable is a bug
// and fails the task.
//
// Resume may itself return *Suspended to re-suspend (e.g. a poll that finds the
// container still running), or a normal (TaskOutput, error) to finalize.
type Resumable interface {
	Resume(ctx context.Context, in TaskInput, s SuspensionState) (TaskOutput, error)
}

// SuspensionState is the rehydrated suspensions row handed to Resume.
type SuspensionState struct {
	Kind    string         // the kind the step suspended with
	Token   string         // callback token (if any)
	Reason  string         // why we resumed: "poll" (scheduled) or "callback"
	Payload map[string]any // the checkpoint the step stored
}
