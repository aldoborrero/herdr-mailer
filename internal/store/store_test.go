package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func fixture(t *testing.T) (*Store, string, string) {
	t.Helper()
	s, e := Open(filepath.Join(t.TempDir(), "state"), "did:key:test")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	_, a, e := s.Register(context.Background(), "alice", "none", "")
	if e != nil {
		t.Fatal(e)
	}
	_, b, e := s.Register(context.Background(), "bob", "none", "")
	if e != nil {
		t.Fatal(e)
	}
	return s, a, b
}
func TestSendAskReplyReadAuthorization(t *testing.T) {
	ctx := context.Background()
	s, a, b := fixture(t)
	if _, e := s.Send(ctx, "fake", "bob", "forged", "send", "", time.Hour, false); !errors.Is(e, ErrUnauthorized) {
		t.Fatal(e)
	}
	ask, e := s.Send(ctx, a, "bob", "question", "ask", "", time.Hour, true)
	if e != nil {
		t.Fatal(e)
	}
	inbox, e := s.Inbox(ctx, "bob", true)
	if e != nil || len(inbox) != 1 || inbox[0].Author != "alice" {
		t.Fatalf("%+v %v", inbox, e)
	}
	if _, e = s.Read(ctx, "alice", ask.ID, true); e == nil {
		t.Fatal("sender read recipient inbox")
	}
	if _, e = s.Send(ctx, a, "", "forged reply", "reply", ask.ID, time.Hour, false); e == nil {
		t.Fatal("unauthorized reply")
	}
	reply, e := s.Send(ctx, b, "", "answer", "reply", ask.ID, time.Hour, false)
	if e != nil || reply.Recipient != "alice" || reply.Correlation != ask.ID {
		t.Fatalf("%+v %v", reply, e)
	}
	if _, e = s.Read(ctx, "bob", ask.ID, true); e != nil {
		t.Fatal(e)
	}
	inbox, e = s.Inbox(ctx, "bob", true)
	if e != nil || len(inbox) != 0 {
		t.Fatal(inbox, e)
	}
	jobs, e := s.Jobs(ctx, "broker", time.Now())
	if e != nil || len(jobs) != 1 {
		t.Fatal(jobs, e)
	}
	if _, _, e = s.Register(ctx, "alice", "none", ""); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Authenticate(ctx, a); !errors.Is(e, ErrUnauthorized) {
		t.Fatal("old credential still works", e)
	}
}
func TestDuplicateRestartAndTransaction(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "s")
	s, e := Open(dir, "did:key:test")
	if e != nil {
		t.Fatal(e)
	}
	_, token, e := s.Register(ctx, "alice", "none", "")
	if e != nil {
		t.Fatal(e)
	}
	m, e := s.Send(ctx, token, "offline", "durable", "send", "", time.Hour, true)
	if e != nil {
		t.Fatal(e)
	}
	if fresh, e := s.Receive(ctx, m); e != nil || fresh {
		t.Fatal(fresh, e)
	}
	bad := m
	bad.Body = "collision"
	if _, e = s.Receive(ctx, bad); e == nil {
		t.Fatal("accepted ID collision")
	}
	if _, e = s.DB.Exec("CREATE TRIGGER fail_outbox BEFORE INSERT ON jobs BEGIN SELECT RAISE(ABORT,'disk full simulation'); END"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Send(ctx, token, "offline", "rollback", "send", "", time.Hour, true); e == nil {
		t.Fatal("expected rollback")
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = Open(dir, "did:key:test")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	inbox, e := s.Inbox(ctx, "offline", false)
	if e != nil || len(inbox) != 1 || inbox[0].ID != m.ID {
		t.Fatal(inbox, e)
	}
	for _, channel := range []string{"native", "broker"} {
		jobs, e := s.Jobs(ctx, channel, time.Now())
		if e != nil || len(jobs) != 1 {
			t.Fatal(channel, jobs, e)
		}
	}
	ev, e := s.Events(ctx, "offline", 0)
	if e != nil || len(ev) != 1 {
		t.Fatal(ev, e)
	}
	ev, e = s.Events(ctx, "alice", 0)
	if e != nil || len(ev) != 0 {
		t.Fatal("event leak", ev, e)
	}
}
func TestReceiveValidationAndExpiryStored(t *testing.T) {
	s, a, _ := fixture(t)
	ctx := context.Background()
	m, e := s.Send(ctx, a, "bob", "body", "send", "", time.Nanosecond, false)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Read(ctx, "bob", m.ID, false); e != nil {
		t.Fatal(e)
	}
	bad := m
	bad.Owner = "other"
	if _, e = s.Receive(ctx, bad); e == nil {
		t.Fatal("untrusted owner")
	}
	bad = m
	bad.Type = "wrong"
	if _, e = s.Receive(ctx, bad); e == nil {
		t.Fatal("invalid schema")
	}
}

func TestOwnerDatabaseBinding(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	s, e := Open(dir, "did:key:owner-one")
	if e != nil {
		t.Fatal(e)
	}
	s.Close()
	if s, e = Open(dir, "did:key:owner-two"); e == nil {
		s.Close()
		t.Fatal("accepted a different owner for an existing database")
	}
}
