package daemon

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRuntimeDisconnectFencesRegistrationAndOldAddress(t *testing.T) {
	s, dir, a, _ := setup(t)
	ctx := context.Background()
	location := uuid.NewString()
	_, token, err := s.Store.Register(ctx, "runtime", "pi", location)
	if err != nil {
		t.Fatal(err)
	}
	cancel, done := start(t, s, dir)
	defer func() { cancel(); <-done }()
	conn, dec, _ := connectRuntime(t, dir, token, "pi", location)
	if _, err = s.Store.Send(ctx, a, "runtime", "in flight", "send", "", time.Hour, false); err != nil {
		t.Fatal(err)
	}
	frame := nextRuntime(t, dec)
	conn.Close()
	rotated := make(chan error, 1)
	go func() {
		_, err := Call(ctx, dir, Request{Op: "register", Token: s.Admin, Agent: "runtime", Runtime: "pi", Location: uuid.NewString()}, nil)
		rotated <- err
	}()
	select {
	case err := <-rotated:
		t.Fatalf("rotated before injection lease expired: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err = <-rotated; err != nil {
		t.Fatal(err)
	}
	if time.Now().Before(frame.Deadline) {
		t.Fatal("routing fence released early")
	}
	if _, err = Call(ctx, dir, Request{Op: "runtime-connect", Token: token, Runtime: "pi", Location: location}, nil); err == nil {
		t.Fatal("old credential reconnected")
	}
	s.Store.DB.Exec("UPDATE jobs SET next=0")
	waitNativeState(t, s, "runtime", "stale")
}
func TestRuntimeWrongAttemptCannotAcknowledge(t *testing.T) {
	s, dir, a, _ := setup(t)
	ctx := context.Background()
	location := uuid.NewString()
	_, token, err := s.Store.Register(ctx, "runtime", "claude", location)
	if err != nil {
		t.Fatal(err)
	}
	cancel, done := start(t, s, dir)
	defer func() { cancel(); <-done }()
	conn, dec, _ := connectRuntime(t, dir, token, "claude", location)
	if _, err = s.Store.Send(ctx, a, "runtime", "forged ack", "send", "", time.Hour, false); err != nil {
		t.Fatal(err)
	}
	frame := nextRuntime(t, dec)
	if err = json.NewEncoder(conn).Encode(runtimeAck{Attempt: uuid.NewString(), Status: "channel_written"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Until(frame.Deadline) + 100*time.Millisecond)
	waitNativeState(t, s, "runtime", "pending")
	status, err := s.Store.Status(ctx, "runtime")
	if err != nil || status["native:channel_written"] != 0 || status["native:accepted"] != 0 {
		t.Fatal(status, err)
	}
}
