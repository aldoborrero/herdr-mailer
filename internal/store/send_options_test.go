package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aldoborrero/herdr-mailer/internal/record"
)

func TestIdempotentSendConcurrentRestartAndRecipientRotation(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "state")
	s, err := Open(dir, "did:key:test")
	if err != nil {
		t.Fatal(err)
	}
	_, a, err := s.Register(ctx, "alice", "none", "")
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := s.Register(ctx, "bob", "none", "")
	if err != nil {
		t.Fatal(err)
	}
	opts := SendOptions{OperationID: "durable-send", RecipientSession: b.ID, RecipientEpoch: b.Epoch}
	var wg sync.WaitGroup
	messages := make(chan record.Message, 32)
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m, e := s.SendWithOptions(ctx, a, "bob", "hello", "send", "", time.Hour, true, opts)
			if e != nil {
				t.Error(e)
				return
			}
			messages <- m
		}()
	}
	wg.Wait()
	close(messages)
	var first []byte
	for m := range messages {
		raw, _ := json.Marshal(m)
		if first == nil {
			first = raw
		} else if string(raw) != string(first) {
			t.Fatal("retry changed message")
		}
	}
	if len(first) == 0 {
		t.Fatal("no accepted send")
	}
	var count int
	for table, want := range map[string]int{"messages": 1, "jobs": 2, "events": 1, "send_operations": 1} {
		if err = s.DB.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != want {
			t.Fatalf("%s: %d %v", table, count, err)
		}
	}
	s.Close()
	s, err = Open(dir, "did:key:test")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, _, err = s.Register(ctx, "bob", "none", ""); err != nil {
		t.Fatal(err)
	}
	replay, err := s.SendWithOptions(ctx, a, "bob", "hello", "send", "", time.Hour, true, opts)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(replay)
	if string(raw) != string(first) {
		t.Fatal("restart or rotation changed original receipt")
	}
	changed := opts
	changed.OperationID = "new-send"
	if _, err = s.SendWithOptions(ctx, a, "bob", "hello", "send", "", time.Hour, true, changed); err == nil {
		t.Fatal("stale recipient accepted")
	}
	if _, err = s.SendWithOptions(ctx, a, "bob", "different", "send", "", time.Hour, true, opts); err == nil {
		t.Fatal("operation collision accepted")
	}
}

func TestIdempotentSendRollsBackEverythingOnOperationFailure(t *testing.T) {
	ctx := context.Background()
	s, a, _ := fixture(t)
	_, err := s.DB.Exec("CREATE TRIGGER fail_operation BEFORE INSERT ON send_operations BEGIN SELECT RAISE(ABORT,'full'); END")
	if err != nil {
		t.Fatal(err)
	}
	opts := SendOptions{OperationID: "retry-after-rollback"}
	if _, err = s.SendWithOptions(ctx, a, "bob", "hi", "send", "", time.Hour, true, opts); err == nil {
		t.Fatal("expected failure")
	}
	for _, table := range []string{"messages", "jobs", "events", "send_operations"} {
		var n int
		if err = s.DB.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("partial %s: %d %v", table, n, err)
		}
	}
	if _, err = s.DB.Exec("DROP TRIGGER fail_operation"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SendWithOptions(ctx, a, "bob", "hi", "send", "", time.Hour, true, opts); err != nil {
		t.Fatal(err)
	}
}

func TestIdempotentReplyAndAuthorIsolation(t *testing.T) {
	ctx := context.Background()
	s, a, b := fixture(t)
	ask, err := s.SendWithOptions(ctx, a, "bob", "question", "ask", "", time.Hour, false, SendOptions{OperationID: "ask-1"})
	if err != nil {
		t.Fatal(err)
	}
	opts := SendOptions{OperationID: "same-key"}
	reply, err := s.SendWithOptions(ctx, b, "", "answer", "reply", ask.ID, time.Hour, false, opts)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.SendWithOptions(ctx, b, "", "answer", "reply", ask.ID, time.Hour, false, opts)
	if err != nil || reply.ID != replay.ID {
		t.Fatal(err)
	}
	if _, err = s.SendWithOptions(ctx, b, "", "other answer", "reply", ask.ID, time.Hour, false, SendOptions{OperationID: "second-reply"}); err == nil {
		t.Fatal("second resolution")
	}
	// The same operation key in a different author's namespace is independent.
	other, err := s.SendWithOptions(ctx, a, "bob", "notice", "send", "", time.Hour, false, opts)
	if err != nil || other.ID == reply.ID {
		t.Fatal(err)
	}
	if _, _, err = s.Register(ctx, "alice", "none", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SendWithOptions(ctx, a, "bob", "notice", "send", "", time.Hour, false, opts); err == nil {
		t.Fatal("retired token replayed operation")
	}
}

func TestRecipientPreconditionValidationDoesNotWrite(t *testing.T) {
	ctx := context.Background()
	s, a, _ := fixture(t)
	for _, opts := range []SendOptions{{OperationID: "../bad"}, {RecipientEpoch: 1}, {RecipientSession: "bad", RecipientEpoch: 1}, {RecipientSession: "00000000-0000-0000-0000-000000000000", RecipientEpoch: 1}} {
		if _, err := s.SendWithOptions(ctx, a, "bob", "hi", "send", "", time.Hour, false, opts); err == nil {
			t.Fatalf("accepted %+v", opts)
		}
	}
	var n int
	s.DB.QueryRow("SELECT count(*) FROM messages").Scan(&n)
	if n != 0 {
		t.Fatal(n)
	}
}
