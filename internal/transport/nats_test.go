package transport

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/aldoborrero/herdr-mailer/internal/record"
	"github.com/aldoborrero/herdr-mailer/internal/store"
	"github.com/aldoborrero/herdr-mailer/internal/testutil"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"net/url"
	"path/filepath"
	"testing"
	"time"
)

func sample() record.Message {
	return record.Message{Type: record.MessageType, ID: uuid.NewString(), Owner: "did:key:test", Author: "alice", Recipient: "bob", Session: uuid.NewString(), Epoch: 1, CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour), Kind: "send", Body: "durable"}
}
func TestAckOnlyAfterPersistenceAndSubjectAuthorization(t *testing.T) {
	m := sample()
	raw, _ := json.Marshal(m)
	acked := false
	failure := errors.New("disk failure")
	ack := func() error { acked = true; return nil }
	persist := func(context.Context, record.Message) (bool, error) { return false, failure }
	if e := Handle(context.Background(), "private.alice.bob", "private", "bob", raw, persist, ack); !errors.Is(e, failure) || acked {
		t.Fatal("ACK before persist", e, acked)
	}
	persist = func(context.Context, record.Message) (bool, error) { return false, nil }
	if e := Handle(context.Background(), "private.mallory.bob", "private", "bob", raw, persist, ack); e == nil || acked {
		t.Fatal("forged sender accepted")
	}
	if e := Handle(context.Background(), "private.alice.bob", "private", "bob", raw, persist, ack); e != nil || !acked {
		t.Fatal("duplicate should ACK after persistence", e)
	}
}
func TestJetStreamOfflineDedupReconnectAndRedelivery(t *testing.T) {
	broker := testutil.NewBroker(t)
	cfg := Config{URL: broker.URL, Stream: "PRIVATE", Prefix: "private"}
	bridge, e := Connect(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer bridge.Close()
	ctx := context.Background()
	m := sample()
	// Publish before any consumer exists: limits retention must preserve this message.
	if e = bridge.Publish(ctx, m); e != nil {
		t.Fatal(e)
	}
	if e = bridge.Publish(ctx, m); e != nil {
		t.Fatal(e)
	}
	info, e := bridge.js.StreamInfo(cfg.Stream)
	if e != nil || info.State.Msgs != 1 {
		t.Fatal("broker dedup failed", info, e)
	}
	sub, e := bridge.Subscribe("bob")
	if e != nil {
		t.Fatal(e)
	}
	cctx, cancel := context.WithTimeout(ctx, time.Second)
	e = bridge.Consume(cctx, sub, "bob", func(context.Context, record.Message) (bool, error) { return false, errors.New("database unavailable") })
	cancel()
	if e == nil {
		t.Fatal("expected persist failure")
	}
	ci, e := sub.ConsumerInfo()
	if e != nil || ci.NumAckPending != 1 {
		t.Fatal("persist failure was acked", ci, e)
	}
	db, e := store.Open(filepath.Join(t.TempDir(), "s"), m.Owner)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	cctx, cancel = context.WithTimeout(ctx, 3*time.Second)
	e = bridge.Consume(cctx, sub, "bob", db.Receive)
	cancel()
	if e != nil {
		t.Fatal(e)
	}
	inbox, e := db.Inbox(ctx, "bob", false)
	if e != nil || len(inbox) != 1 {
		t.Fatal(inbox, e)
	}
	// Restart the same private server with persistent storage and keep the client connection.
	broker.Stop()
	m2 := sample()
	cctx, cancel = context.WithTimeout(ctx, 150*time.Millisecond)
	e = bridge.Publish(cctx, m2)
	cancel()
	if e == nil {
		t.Fatal("offline publish unexpectedly succeeded")
	}
	broker.Start(t)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		cctx, cancel = context.WithTimeout(ctx, 300*time.Millisecond)
		e = bridge.Publish(cctx, m2)
		cancel()
		if e == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if e != nil {
		t.Fatal("reconnect failed", e)
	}
	cctx, cancel = context.WithTimeout(ctx, 3*time.Second)
	e = bridge.Consume(cctx, sub, "bob", db.Receive)
	cancel()
	if e != nil {
		t.Fatal(e)
	}
	inbox, e = db.Inbox(ctx, "bob", false)
	if e != nil || len(inbox) != 2 {
		t.Fatal(inbox, e)
	}
	// A second replay at the SQLite layer remains idempotent after broker dedup windows.
	if fresh, e := db.Receive(ctx, m2); e != nil || fresh {
		t.Fatal(fresh, e)
	}
}

func TestPollDoesNotExpirePersistenceWithIdleFetchDeadline(t *testing.T) {
	broker := testutil.NewBroker(t)
	bridge, e := Connect(Config{URL: broker.URL, Stream: "POLL", Prefix: "poll"})
	if e != nil {
		t.Fatal(e)
	}
	defer bridge.Close()
	ctx := context.Background()
	sub, e := bridge.Subscribe("bob")
	if e != nil {
		t.Fatal(e)
	}
	if e = bridge.Publish(ctx, sample()); e != nil {
		t.Fatal(e)
	}
	e = bridge.Poll(ctx, sub, "bob", func(ctx context.Context, m record.Message) (bool, error) {
		time.Sleep(50 * time.Millisecond)
		return true, ctx.Err()
	})
	if e != nil {
		t.Fatal("short idle timeout leaked into persistence", e)
	}
	info, e := sub.ConsumerInfo()
	if e != nil || info.NumAckPending != 0 {
		t.Fatal(info, e)
	}
}

func TestRejectDestructivePerSubjectRetention(t *testing.T) {
	broker := testutil.NewBroker(t)
	cfg := Config{URL: broker.URL, Stream: "LIMITS", Prefix: "limits"}
	bridge, e := Connect(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer bridge.Close()
	info, e := bridge.js.StreamInfo(cfg.Stream)
	if e != nil {
		t.Fatal(e)
	}
	sc := info.Config
	sc.MaxMsgsPerSubject = 1
	if _, e = bridge.js.UpdateStream(&sc); e != nil {
		t.Fatal(e)
	}
	if unsafe, e := Connect(cfg); e == nil {
		unsafe.Close()
		t.Fatal("accepted implicit discard-old at the subject limit")
	}
	sc.DiscardNewPerSubject = true
	if _, e = bridge.js.UpdateStream(&sc); e != nil {
		t.Fatal(e)
	}
	safe, e := Connect(cfg)
	if e != nil {
		t.Fatal(e)
	}
	safe.Close()
}

func TestUntrustedEnvelopeNeverPersistsOrAcknowledges(t *testing.T) {
	ctx := context.Background()
	m := sample()
	db, e := store.Open(filepath.Join(t.TempDir(), "s"), m.Owner)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	raw, _ := json.Marshal(m)
	unknown := append(append([]byte{}, raw[:len(raw)-1]...), []byte(`,"fromAgent":"mallory"}`)...)
	wrongOwner := m
	wrongOwner.Owner = "did:key:other"
	foreign, _ := json.Marshal(wrongOwner)
	for _, tc := range []struct {
		name, subject, recipient string
		body                     []byte
	}{
		{"owner", "private.alice.bob", "bob", foreign},
		{"author", "private.mallory.bob", "bob", raw},
		{"recipient", "private.alice.mallory", "mallory", raw},
		{"route", "other.alice.bob", "bob", raw},
		{"unknown field", "private.alice.bob", "bob", unknown},
		{"trailing JSON", "private.alice.bob", "bob", append(append([]byte{}, raw...), []byte(` {}`)...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			acked := false
			e := Handle(ctx, tc.subject, "private", tc.recipient, tc.body, db.Receive, func() error { acked = true; return nil })
			if e == nil || acked {
				t.Fatal("accepted untrusted envelope", e, acked)
			}
		})
	}
	var count int
	if e = db.DB.QueryRow("SELECT count(*) FROM messages").Scan(&count); e != nil || count != 0 {
		t.Fatal(count, e)
	}
}

func TestBrokerDedupIncludesOrigin(t *testing.T) {
	broker := testutil.NewBroker(t)
	bridge, e := Connect(Config{URL: broker.URL, Stream: "ORIGINS", Prefix: "origins"})
	if e != nil {
		t.Fatal(e)
	}
	defer bridge.Close()
	ctx := context.Background()
	m := sample()
	other := m
	other.Author = "carol"
	other.Body = "another origin"
	for _, msg := range []record.Message{m, m, other, other} {
		if e = bridge.Publish(ctx, msg); e != nil {
			t.Fatal(e)
		}
	}
	info, e := bridge.js.StreamInfo("ORIGINS")
	if e != nil || info.State.Msgs != 2 {
		t.Fatal("broker conflated origins", info, e)
	}
	sub, e := bridge.Subscribe("bob")
	if e != nil {
		t.Fatal(e)
	}
	db, e := store.Open(filepath.Join(t.TempDir(), "s"), m.Owner)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	cctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if e = bridge.Consume(cctx, sub, "bob", db.Receive); e != nil {
		t.Fatal(e)
	}
	inbox, e := db.Inbox(ctx, "bob", false)
	if e != nil || len(inbox) != 2 {
		t.Fatal(inbox, e)
	}
}

func TestReplyNotAcknowledgedUntilAuthorizedAskExists(t *testing.T) {
	broker := testutil.NewBroker(t)
	bridge, e := Connect(Config{URL: broker.URL, Stream: "REPLIES", Prefix: "replies"})
	if e != nil {
		t.Fatal(e)
	}
	defer bridge.Close()
	ctx := context.Background()
	ask := sample()
	ask.Kind = "ask"
	reply := sample()
	reply.Kind = "reply"
	reply.Author = "bob"
	reply.Recipient = "alice"
	reply.Correlation = ask.ID
	db, e := store.Open(filepath.Join(t.TempDir(), "s"), ask.Owner)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	sub, e := bridge.Subscribe("alice")
	if e != nil {
		t.Fatal(e)
	}
	if e = bridge.Publish(ctx, reply); e != nil {
		t.Fatal(e)
	}
	cctx, cancel := context.WithTimeout(ctx, time.Second)
	e = bridge.Consume(cctx, sub, "alice", db.Receive)
	cancel()
	if e == nil {
		t.Fatal("reply without ask was accepted")
	}
	info, e := sub.ConsumerInfo()
	if e != nil || info.NumAckPending != 1 {
		t.Fatal("reply ACK preceded authorization", info, e)
	}
	inbox, e := db.Inbox(ctx, "alice", false)
	if e != nil || len(inbox) != 0 {
		t.Fatal("unauthorized reply persisted", inbox, e)
	}
	if _, e = db.Receive(ctx, ask); e != nil {
		t.Fatal(e)
	}
	cctx, cancel = context.WithTimeout(ctx, 3*time.Second)
	e = bridge.Consume(cctx, sub, "alice", db.Receive)
	cancel()
	if e != nil {
		t.Fatal(e)
	}
	info, e = sub.ConsumerInfo()
	if e != nil || info.NumAckPending != 0 {
		t.Fatal(info, e)
	}
	inbox, e = db.Inbox(ctx, "alice", false)
	if e != nil || len(inbox) != 1 {
		t.Fatal(inbox, e)
	}
}

func TestPrivateBrokerRejectsUnauthorizedPublishers(t *testing.T) {
	broker := testutil.NewBrokerWithConfig(t, `authorization {
 users: [
  {user: bridge, password: bridge-test-only, permissions: {publish: ["private.alice.*", "$JS.API.>", "$JS.ACK.>"], subscribe: ["_INBOX.>"]}},
  {user: worker, password: worker-test-only, permissions: {publish: ["_INBOX.>"], subscribe: ["_INBOX.>"]}}
 ]
}`, "bridge", "bridge-test-only")
	bridge, e := Connect(Config{URL: broker.URL, Stream: "PRIVATE_ACL", Prefix: "private"})
	if e != nil {
		t.Fatal(e)
	}
	defer bridge.Close()
	m := sample()
	if e = bridge.Publish(context.Background(), m); e != nil {
		t.Fatal(e)
	}
	denied := make(chan error, 4)
	u, e := url.Parse(broker.URL)
	if e != nil {
		t.Fatal(e)
	}
	u.User = url.UserPassword("worker", "worker-test-only")
	worker, e := nats.Connect(u.String(), nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, e error) { denied <- e }))
	if e != nil {
		t.Fatal(e)
	}
	defer worker.Close()
	raw, _ := json.Marshal(m)
	if e = worker.Publish("private.alice.bob", raw); e != nil {
		t.Fatal(e)
	}
	_ = worker.Flush()
	select {
	case e = <-denied:
		if !errors.Is(e, nats.ErrPermissionViolation) {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("worker could publish to trusted subject")
	}
	// This daemon principal is also constrained to its configured author routes.
	bridge.conn.SetErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, e error) { denied <- e })
	if e = bridge.conn.Publish("private.mallory.bob", raw); e != nil {
		t.Fatal(e)
	}
	_ = bridge.conn.Flush()
	select {
	case e = <-denied:
		if !errors.Is(e, nats.ErrPermissionViolation) {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("bridge escaped its publication ACL")
	}
	info, e := bridge.js.StreamInfo("PRIVATE_ACL")
	if e != nil || info.State.Msgs != 1 {
		t.Fatal(info, e)
	}
}
