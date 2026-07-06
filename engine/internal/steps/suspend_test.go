package steps

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestSuspendErrorFormatting pins the sentinel's Error() text and the errors.As
// detection the engine relies on to tell a suspension apart from a failure.
func TestSuspendErrorFormatting(t *testing.T) {
	err := error(&Suspend{Kind: "callback"})
	if err.Error() != "suspend: callback" {
		t.Fatalf("Error() = %q, want %q", err.Error(), "suspend: callback")
	}

	var s *Suspend
	if !errors.As(err, &s) {
		t.Fatal("errors.As should detect a *Suspend")
	}
	if s.Kind != "callback" {
		t.Fatalf("recovered Kind = %q, want callback", s.Kind)
	}

	// A plain error must not be mistaken for a suspension.
	if errors.As(errors.New("boom"), &s) {
		t.Fatal("errors.As must not match a non-Suspend error")
	}
}

// TestDelaySuspendsLongWait: a wait over the in-process ceiling returns a
// delay-kind Suspend with a future ResumeAt and the resume output.
func TestDelaySuspendsLongWait(t *testing.T) {
	before := time.Now()
	_, err := (&Delay{}).Execute(context.Background(), TaskInput{Config: map[string]any{"seconds": float64(600)}})
	var s *Suspend
	if !errors.As(err, &s) {
		t.Fatalf("delay 600s should suspend, got err %v", err)
	}
	if s.Kind != "delay" {
		t.Fatalf("Kind = %q, want delay", s.Kind)
	}
	if s.ResumeAt == nil {
		t.Fatal("delay suspension must carry a ResumeAt")
	}
	if s.ResumeAt.Before(before.Add(590*time.Second)) || s.ResumeAt.After(before.Add(610*time.Second)) {
		t.Fatalf("ResumeAt = %v, want ~now+600s", s.ResumeAt)
	}
	out, _ := s.Output.(map[string]any)
	if out["waited_seconds"] != float64(600) {
		t.Fatalf("Output.waited_seconds = %#v, want 600", out["waited_seconds"])
	}
}

// TestDelayInProcessShort: a short wait runs in-process and returns normally.
func TestDelayInProcessShort(t *testing.T) {
	out, err := (&Delay{}).Execute(context.Background(), TaskInput{Config: map[string]any{"seconds": 0.01}})
	if err != nil {
		t.Fatalf("short delay should not error: %v", err)
	}
	data, _ := out.Data.(map[string]any)
	if data["waited_seconds"] != 0.01 {
		t.Fatalf("waited_seconds = %#v, want 0.01", data["waited_seconds"])
	}
}

// TestDelayRejectsNonPositive keeps the guard on seconds ≤ 0.
func TestDelayRejectsNonPositive(t *testing.T) {
	if _, err := (&Delay{}).Execute(context.Background(), TaskInput{Config: map[string]any{"seconds": 0}}); err == nil {
		t.Fatal("delay with seconds=0 should error")
	}
}

// TestDelayRejectsOverMax rejects a wait beyond the 30-day ceiling.
func TestDelayRejectsOverMax(t *testing.T) {
	over := (31 * 24 * time.Hour).Seconds()
	_, err := (&Delay{}).Execute(context.Background(), TaskInput{Config: map[string]any{"seconds": over}})
	if err == nil {
		t.Fatal("delay beyond 30 days should error")
	}
	var s *Suspend
	if errors.As(err, &s) {
		t.Fatal("an over-max delay is a failure, not a suspension")
	}
}

// TestWaitCallbackSuspends: the callback step suspends immediately with a
// callback-kind Suspend, no ResumeAt, carrying its note.
func TestWaitCallbackSuspends(t *testing.T) {
	_, err := (&WaitCallback{}).Execute(context.Background(), TaskInput{Config: map[string]any{"note": "approve the deploy"}})
	var s *Suspend
	if !errors.As(err, &s) {
		t.Fatalf("wait.callback should suspend, got err %v", err)
	}
	if s.Kind != "callback" {
		t.Fatalf("Kind = %q, want callback", s.Kind)
	}
	if s.ResumeAt != nil {
		t.Fatal("callback suspension must not carry a ResumeAt")
	}
	out, _ := s.Output.(map[string]any)
	if out["waiting"] != true {
		t.Fatalf("Output.waiting = %#v, want true", out["waiting"])
	}
	if out["note"] != "approve the deploy" {
		t.Fatalf("Output.note = %#v, want the config note", out["note"])
	}
}
