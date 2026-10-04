package daemon

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/aldoborrero/herdr-mailer/internal/record"
	"github.com/google/uuid"
)

type runtimeFrame struct {
	Type     string         `json:"type"`
	Attempt  string         `json:"attempt"`
	Deadline time.Time      `json:"deadline"`
	Session  record.Session `json:"session"`
	Message  record.Message `json:"message"`
}

func connectRuntime(t *testing.T, dir, token, runtime, location string) (net.Conn, *json.Decoder, runtimeFrame) {
	t.Helper()
	conn, err := net.Dial("unix", filepath.Join(dir, "daemon.sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err = conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err = json.NewEncoder(conn).Encode(Request{Op: "runtime-connect", Token: token, Runtime: runtime, Location: location}); err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(conn)
	var response struct {
		Data  runtimeFrame `json:"data"`
		Error string       `json:"error"`
	}
	if err = dec.Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Error != "" {
		t.Fatal(response.Error)
	}
	return conn, dec, response.Data
}
func nextRuntime(t *testing.T, dec *json.Decoder) runtimeFrame {
	t.Helper()
	var response struct {
		Data  runtimeFrame `json:"data"`
		Error string       `json:"error"`
	}
	if err := dec.Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Error != "" || response.Data.Type != "delivery" {
		t.Fatalf("%+v", response)
	}
	return response.Data
}
func waitNativeState(t *testing.T, s *Server, agent, state string) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		status, err := s.Store.Status(context.Background(), agent)
		if err != nil {
			t.Fatal(err)
		}
		if status["native:"+state] == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	status, _ := s.Store.Status(context.Background(), agent)
	t.Fatal(status)
}
func TestNativeRuntimeAuthenticatedDeliveryAndReceipts(t *testing.T) {
	for _, runtime := range []string{"pi", "claude"} {
		t.Run(runtime, func(t *testing.T) {
			s, dir, a, _ := setup(t)
			ctx := context.Background()
			location := uuid.NewString()
			target, token, err := s.Store.Register(ctx, "runtime", runtime, location)
			if err != nil {
				t.Fatal(err)
			}
			cancel, done := start(t, s, dir)
			defer func() { cancel(); <-done }()
			for _, r := range []Request{
				{Op: "runtime-connect", Token: a, Runtime: runtime, Location: location},
				{Op: "runtime-connect", Token: token, Runtime: runtime, Location: uuid.NewString()},
				{Op: "runtime-connect", Token: token, Runtime: "codex", Location: location},
			} {
				if _, err = Call(ctx, dir, r, nil); err == nil {
					t.Fatal("invalid bridge authorized")
				}
			}
			m, err := s.Store.Send(ctx, a, "runtime", "automatic", "send", "", time.Hour, false)
			if err != nil {
				t.Fatal(err)
			}
			conn, dec, ready := connectRuntime(t, dir, token, runtime, location)
			if ready.Type != "ready" || ready.Session.ID != target.ID {
				t.Fatal(ready)
			}
			if _, err = Call(ctx, dir, Request{Op: "runtime-connect", Token: token, Runtime: runtime, Location: location}, nil); err == nil {
				t.Fatal("second connection accepted")
			}
			frame := nextRuntime(t, dec)
			if frame.Message.ID != m.ID || frame.Session.Epoch != target.Epoch || !time.Now().Before(frame.Deadline) {
				t.Fatal(frame)
			}
			status := "accepted"
			if runtime == "claude" {
				status = "channel_written"
			}
			if err = json.NewEncoder(conn).Encode(runtimeAck{Attempt: frame.Attempt, Status: status}); err != nil {
				t.Fatal(err)
			}
			waitNativeState(t, s, "runtime", status)
			inbox, err := s.Store.Inbox(ctx, "runtime", true)
			if err != nil || len(inbox) != 1 {
				t.Fatal(inbox, err)
			}
			// Delivery events and reads must not create another native job/frame.
			if _, err = s.Store.Read(ctx, "runtime", m.ID, true); err != nil {
				t.Fatal(err)
			}
			conn.SetReadDeadline(time.Now().Add(400 * time.Millisecond))
			var extra any
			if dec.Decode(&extra) == nil {
				t.Fatal("own status/read event reinjected")
			}
		})
	}
}
func TestRuntimeDisconnectRetainsAndReconnectReplays(t *testing.T) {
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
	m, err := s.Store.Send(ctx, a, "runtime", "disconnect", "send", "", time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	first := nextRuntime(t, dec)
	conn.Close()
	// Let the bounded injection lease expire before attempting a reconnect.
	time.Sleep(time.Until(first.Deadline) + 50*time.Millisecond)
	conn, dec, _ = connectRuntime(t, dir, token, "pi", location)
	second := nextRuntime(t, dec)
	if second.Message.ID != m.ID || first.Attempt == second.Attempt {
		t.Fatal("not a replay with a fresh attempt fence")
	}
	json.NewEncoder(conn).Encode(runtimeAck{Attempt: second.Attempt, Status: "accepted"})
	waitNativeState(t, s, "runtime", "accepted")
}
func TestRuntimeOfflineNeverExhaustsAndRotationIsStale(t *testing.T) {
	s, _, a, _ := setup(t)
	ctx := context.Background()
	location := uuid.NewString()
	_, _, err := s.Store.Register(ctx, "runtime", "pi", location)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Store.Send(ctx, a, "runtime", "offline", "send", "", time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		s.Store.DB.Exec("UPDATE jobs SET next=0")
		if err = s.ProcessNative(ctx); err != nil {
			t.Fatal(err)
		}
	}
	waitNativeState(t, s, "runtime", "pending")
	_, _, err = s.Store.Register(ctx, "runtime", "pi", uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	s.Store.DB.Exec("UPDATE jobs SET next=0")
	if err = s.ProcessNative(ctx); err != nil {
		t.Fatal(err)
	}
	waitNativeState(t, s, "runtime", "stale")
}
