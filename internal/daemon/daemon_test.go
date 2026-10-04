package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aldoborrero/herdr-mailer/internal/delivery"
	"github.com/aldoborrero/herdr-mailer/internal/record"
	"github.com/aldoborrero/herdr-mailer/internal/store"
)

type fakeAdapter struct {
	calls atomic.Int32
	err   error
}

func (f *fakeAdapter) Deliver(context.Context, record.Session, record.Message) error {
	f.calls.Add(1)
	return f.err
}
func setup(t *testing.T) (*Server, string, string, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "s")
	db, e := store.Open(dir, "did:key:test")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	_, a, e := db.Register(ctx, "alice", "none", "")
	if e != nil {
		t.Fatal(e)
	}
	_, b, e := db.Register(ctx, "bob", "codex", "01a0f9be-cb70-7390-bcad-7c50b52940be")
	if e != nil {
		t.Fatal(e)
	}
	return &Server{Store: db, Admin: "admin", Adapter: &fakeAdapter{}}, dir, a, b
}
func start(t *testing.T, s *Server, dir string) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, dir) }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, e := os.Stat(filepath.Join(dir, "daemon.sock")); e == nil {
			t.Cleanup(cancel)
			return cancel, done
		}
		select {
		case e := <-done:
			cancel()
			t.Fatal(e)
		default:
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	t.Fatal("socket never ready")
	return nil, nil
}
func TestSocketBoundariesEventsAndShutdown(t *testing.T) {
	s, dir, a, b := setup(t)
	cancel, done := start(t, s, dir)
	ctx := context.Background()
	ask, e := Call(ctx, dir, Request{Op: "ask", Token: a, Recipient: "bob", Body: "question"}, nil)
	if e != nil {
		t.Fatal(e)
	}
	var m record.Message
	if e = json.Unmarshal(ask, &m); e != nil {
		t.Fatal(e)
	}
	for _, r := range []Request{{Op: "inbox", Token: "fake"}, {Op: "read", Token: a, ID: m.ID}, {Op: "reply", Token: a, ID: m.ID, Body: "forged"}, {Op: "register", Token: a, Agent: "mallory", Runtime: "none"}} {
		if _, e = Call(ctx, dir, r, nil); e == nil {
			t.Fatal("unauthorized request succeeded", r.Op)
		}
	}
	if _, e = Call(ctx, dir, Request{Op: "reply", Token: b, ID: m.ID, Body: "answer"}, nil); e != nil {
		t.Fatal(e)
	}
	conn, e := net.Dial("unix", filepath.Join(dir, "daemon.sock"))
	if e != nil {
		t.Fatal(e)
	}
	_, _ = conn.Write([]byte(`{"op":"send","token":"` + a + `","fromAgent":"bob","recipient":"bob","body":"forged"}` + "\n"))
	var rejected Response
	if e = json.NewDecoder(conn).Decode(&rejected); e != nil || rejected.Error == "" {
		t.Fatal(rejected, e)
	}
	conn.Close()
	eventCtx, stopEvents := context.WithCancel(ctx)
	eventDone := make(chan error, 1)
	seen := make(chan store.Event, 20)
	go func() {
		_, e := Call(eventCtx, dir, Request{Op: "events", Token: b, Follow: true}, func(raw json.RawMessage) error {
			var ev store.Event
			if e := json.Unmarshal(raw, &ev); e != nil {
				return e
			}
			seen <- ev
			return nil
		})
		eventDone <- e
	}()
	select {
	case ev := <-seen:
		if ev.MessageID != m.ID {
			t.Fatal(ev)
		}
	case <-time.After(time.Second):
		t.Fatal("missing recipient event")
	}
	stopEvents()
	select {
	case <-eventDone:
	case <-time.After(time.Second):
		t.Fatal("client cancellation blocked")
	}
	incomplete, e := net.Dial("unix", filepath.Join(dir, "daemon.sock"))
	if e != nil {
		t.Fatal(e)
	}
	defer incomplete.Close()
	_, _ = incomplete.Write([]byte(`{"op":`))
	cancel()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("incomplete socket request blocked shutdown")
	}
}
func TestNativeEpochExpiryAndDurableRetries(t *testing.T) {
	ctx := context.Background()
	s, _, a, _ := setup(t)
	fake := s.Adapter.(*fakeAdapter)
	first, e := s.Store.Send(ctx, a, "bob", "stale", "send", "", time.Hour, false)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.Store.Register(ctx, "bob", "codex", "01a0f9be-cb70-7390-bcad-7c50b52940be"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Store.Send(ctx, a, "bob", "expired", "send", "", time.Nanosecond, false); e != nil {
		t.Fatal(e)
	}
	if e = s.ProcessNative(ctx); e != nil {
		t.Fatal(e)
	}
	if fake.calls.Load() != 0 {
		t.Fatal("injected stale/expired message")
	}
	status, e := s.Store.Status(ctx, "bob")
	if e != nil || status["native:stale"] != 1 || status["native:expired"] != 1 {
		t.Fatal(status, e)
	}
	if _, e = s.Store.Send(ctx, a, "bob", "retry", "send", "", time.Hour, false); e != nil {
		t.Fatal(e)
	}
	fake.err = errors.New("temporary failure")
	if e = s.ProcessNative(ctx); e != nil {
		t.Fatal(e)
	}
	jobs, e := s.Store.Jobs(ctx, "native", time.Now())
	if e != nil || len(jobs) != 0 {
		t.Fatal("backoff not applied", jobs, e)
	}
	var attempts int
	var state string
	if e = s.Store.DB.QueryRow("SELECT attempts,state FROM jobs WHERE id=?", first.DeliveryID()).Scan(&attempts, &state); e != nil || state != "stale" {
		t.Fatal(e, state)
	}
	if _, e = s.Store.DB.Exec("UPDATE jobs SET next=0 WHERE state='pending'"); e != nil {
		t.Fatal(e)
	}
	fake.err = nil
	if e = s.ProcessNative(ctx); e != nil {
		t.Fatal(e)
	}
	status, e = s.Store.Status(ctx, "bob")
	if e != nil || status["native:accepted"] != 1 {
		t.Fatal(status, e)
	}
	if _, e = s.Store.Send(ctx, a, "bob", "ambiguous", "send", "", time.Hour, false); e != nil {
		t.Fatal(e)
	}
	fake.err = delivery.ErrAmbiguous
	if e = s.ProcessNative(ctx); e != nil {
		t.Fatal(e)
	}
	status, e = s.Store.Status(ctx, "bob")
	if e != nil || status["native:ambiguous"] != 1 {
		t.Fatal(status, e)
	}
}
func TestOfflineBindingAndRestartReplay(t *testing.T) {
	ctx := context.Background()
	s, dir, a, _ := setup(t)
	if _, e := s.Store.Send(ctx, a, "offline", "pending", "send", "", time.Hour, false); e != nil {
		t.Fatal(e)
	}
	if e := s.ProcessNative(ctx); e != nil {
		t.Fatal(e)
	}
	if e := s.Store.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := store.Open(dir, "did:key:test")
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	s.Store = reopened
	if _, _, e = reopened.Register(ctx, "offline", "codex", "01a0f9be-cb70-7390-bcad-7c50b52940be"); e != nil {
		t.Fatal(e)
	}
	if _, e = reopened.DB.Exec("UPDATE jobs SET next=0"); e != nil {
		t.Fatal(e)
	}
	if e = s.ProcessNative(ctx); e != nil {
		t.Fatal(e)
	}
	if s.Adapter.(*fakeAdapter).calls.Load() != 1 {
		t.Fatal("not replayed")
	}
}

func TestEventBacklogAndRevokedSubscription(t *testing.T) {
	s, dir, a, b := setup(t)
	ctx := context.Background()
	for i := 0; i < 105; i++ {
		if _, e := s.Store.Send(ctx, a, "bob", "event", "send", "", time.Hour, false); e != nil {
			t.Fatal(e)
		}
	}
	cancel, done := start(t, s, dir)
	defer func() { cancel(); <-done }()
	count := 0
	_, e := Call(ctx, dir, Request{Op: "events", Token: b}, func(raw json.RawMessage) error {
		var event store.Event
		if e := json.Unmarshal(raw, &event); e != nil {
			return e
		}
		if event.Kind == "received" {
			count++
		}
		return nil
	})
	if e != nil || count != 105 {
		t.Fatal("truncated event backlog", count, e)
	}
	streamCtx, stop := context.WithCancel(ctx)
	defer stop()
	streamDone := make(chan error, 1)
	ready := make(chan struct{}, 1)
	go func() {
		_, e := Call(streamCtx, dir, Request{Op: "events", Token: b, Follow: true}, func(json.RawMessage) error {
			select {
			case ready <- struct{}{}:
			default:
			}
			return nil
		})
		streamDone <- e
	}()
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("stream not ready")
	}
	if _, e = Call(ctx, dir, Request{Op: "register", Token: "admin", Agent: "bob", Runtime: "none"}, nil); e != nil {
		t.Fatal(e)
	}
	select {
	case e = <-streamDone:
		if e == nil {
			t.Fatal("revoked stream ended without error")
		}
	case <-time.After(time.Second):
		t.Fatal("revoked subscription not cancelled")
	}
}

type adapterFunc func(context.Context, record.Session, record.Message) error

func (f adapterFunc) Deliver(ctx context.Context, s record.Session, m record.Message) error {
	return f(ctx, s, m)
}
func TestShutdownPersistsAmbiguousOutcome(t *testing.T) {
	s, _, a, _ := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, e := s.Store.Send(ctx, a, "bob", "cancel during queue", "send", "", time.Hour, false); e != nil {
		t.Fatal(e)
	}
	s.Adapter = adapterFunc(func(context.Context, record.Session, record.Message) error { cancel(); return delivery.ErrAmbiguous })
	if e := s.ProcessNative(ctx); e != nil {
		t.Fatal(e)
	}
	status, e := s.Store.Status(context.Background(), "bob")
	if e != nil || status["native:ambiguous"] != 1 {
		t.Fatal(status, e)
	}
}
func TestNativeRetryLimitAndUnsupportedRuntime(t *testing.T) {
	s, _, a, _ := setup(t)
	ctx := context.Background()
	fake := s.Adapter.(*fakeAdapter)
	fake.err = errors.New("queue failed")
	if _, e := s.Store.Send(ctx, a, "bob", "retry limit", "send", "", time.Hour, false); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 9; i++ {
		if _, e := s.Store.DB.Exec("UPDATE jobs SET next=0"); e != nil {
			t.Fatal(e)
		}
		if e := s.ProcessNative(ctx); e != nil {
			t.Fatal(e)
		}
	}
	status, e := s.Store.Status(ctx, "bob")
	if e != nil || status["native:failed"] != 1 || fake.calls.Load() != 8 {
		t.Fatal(status, fake.calls.Load(), e)
	}
	if _, _, e = s.Store.Register(ctx, "pi-agent", "pi", ""); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Store.Send(ctx, a, "pi-agent", "unsupported", "send", "", time.Hour, false); e != nil {
		t.Fatal(e)
	}
	s.Adapter = delivery.Codex{}
	if e = s.ProcessNative(ctx); e != nil {
		t.Fatal(e)
	}
	status, e = s.Store.Status(ctx, "pi-agent")
	if e != nil || status["native:unsupported"] != 1 {
		t.Fatal(status, e)
	}
}

func TestDelayedBrokerRecordKeepsExplicitRecipientSession(t *testing.T) {
	s, _, a, _ := setup(t)
	ctx := context.Background()
	first, e := s.Store.Send(ctx, a, "bob", "in flight", "send", "", time.Hour, true)
	if e != nil {
		t.Fatal(e)
	}
	if first.RecipientEpoch == 0 || first.RecipientSession == "" {
		t.Fatal("sender did not pin target in record")
	}
	delayed := first
	delayed.ID = "00000000-0000-4000-8000-000000000099"
	replacement, _, e := s.Store.Register(ctx, "bob", "codex", "01a0f9be-cb70-7390-bcad-7c50b52940be")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Store.Receive(ctx, delayed); e != nil {
		t.Fatal(e)
	}
	// Even an equal epoch with a different session UUID must be refused.
	wrongID := delayed
	wrongID.ID = "00000000-0000-4000-8000-000000000098"
	wrongID.RecipientEpoch = replacement.Epoch
	if _, e = s.Store.Receive(ctx, wrongID); e != nil {
		t.Fatal(e)
	}
	if e = s.ProcessNative(ctx); e != nil {
		t.Fatal(e)
	}
	status, e := s.Store.Status(ctx, "bob")
	if e != nil || status["native:stale"] != 3 || s.Adapter.(*fakeAdapter).calls.Load() != 0 {
		t.Fatal(status, e)
	}
}

func TestRegistrationSerializedWithNativeInjection(t *testing.T) {
	s, _, a, _ := setup(t)
	ctx := context.Background()
	if _, e := s.Store.Send(ctx, a, "bob", "serialize", "send", "", time.Hour, false); e != nil {
		t.Fatal(e)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	workerDone := make(chan error, 1)
	s.Adapter = adapterFunc(func(ctx context.Context, binding record.Session, m record.Message) error {
		current, e := s.Store.Session(ctx, m.Recipient)
		if e != nil || current.ID != binding.ID || current.Epoch != m.RecipientEpoch {
			return errors.New("stale binding at injection")
		}
		close(entered)
		<-release
		return nil
	})
	go func() { workerDone <- s.ProcessNative(ctx) }()
	<-entered
	registered := make(chan error, 1)
	go func() {
		_, e := s.dispatch(ctx, Request{Op: "register", Token: "admin", Agent: "bob", Runtime: "none"})
		registered <- e
	}()
	select {
	case e := <-registered:
		close(release)
		<-workerDone
		t.Fatal("session rotated while injection was in flight", e)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if e := <-workerDone; e != nil {
		t.Fatal(e)
	}
	if e := <-registered; e != nil {
		t.Fatal(e)
	}
}
