package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aldoborrero/herdr-mailer/internal/identity"
	"github.com/aldoborrero/herdr-mailer/internal/record"
	"github.com/google/uuid"
)

func TestOriginScopedDedupRestartAndAmbiguousRead(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "s")
	s, e := Open(dir, "did:key:test")
	if e != nil {
		t.Fatal(e)
	}
	_, a, e := s.Register(ctx, "alice", "none", "")
	if e != nil {
		t.Fatal(e)
	}
	_, bob, e := s.Register(ctx, "bob", "none", "")
	if e != nil {
		t.Fatal(e)
	}
	m, e := s.Send(ctx, a, "bob", "ask alice", "ask", "", time.Hour, false)
	if e != nil {
		t.Fatal(e)
	}
	other := m
	other.Author = "carol"
	other.Session = uuid.NewString()
	other.Body = "ask carol"
	if fresh, e := s.Receive(ctx, other); e != nil || !fresh {
		t.Fatal("same UUID from another origin must coexist", fresh, e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = Open(dir, "did:key:test")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	for _, msg := range []record.Message{m, other} {
		if fresh, e := s.Receive(ctx, msg); e != nil || fresh {
			t.Fatal("persistent dedup", fresh, e)
		}
		changed := msg
		changed.Body = "collision"
		if _, e = s.Receive(ctx, changed); e == nil {
			t.Fatal("accepted changed content for the same origin and ID")
		}
	}
	if _, e = s.Read(ctx, "bob", m.ID, true); e == nil {
		t.Fatal("ambiguous ID selected an arbitrary origin")
	}
	if _, e = s.Send(ctx, bob, "", "ambiguous answer", "reply", m.ID, time.Hour, false); e == nil {
		t.Fatal("ambiguous reply accepted")
	}
	chosen, e := s.Read(ctx, "bob", m.ID, true, "alice")
	if e != nil || chosen.Author != "alice" {
		t.Fatal(chosen, e)
	}
	inbox, e := s.Inbox(ctx, "bob", true)
	if e != nil || len(inbox) != 1 || inbox[0].Author != "carol" {
		t.Fatal("read marked another origin", inbox, e)
	}
	reply, e := s.Send(ctx, bob, "", "answer to carol", "reply", m.ID, time.Hour, false, "carol")
	if e != nil || reply.Recipient != "carol" {
		t.Fatal(reply, e)
	}
	events, e := s.Events(ctx, "bob", 0)
	if e != nil || len(events) != 2 || events[0].Origin == events[1].Origin {
		t.Fatal(events, e)
	}
	jobs, e := s.Jobs(ctx, "native", time.Now())
	if e != nil || len(jobs) != 3 {
		t.Fatal(jobs, e)
	}
	if e = s.Finish(ctx, jobs[0], "accepted", ""); e != nil {
		t.Fatal(e)
	}
	jobs, e = s.Jobs(ctx, "native", time.Now())
	if e != nil || len(jobs) != 2 {
		t.Fatal("finish crossed origin boundary", jobs, e)
	}
}

func TestIncomingReplyAuthorizationBeforePersistence(t *testing.T) {
	s, alice, bob := fixture(t)
	ctx := context.Background()
	ask, e := s.Send(ctx, alice, "bob", "question", "ask", "", time.Hour, false)
	if e != nil {
		t.Fatal(e)
	}
	valid := record.Message{Type: record.MessageType, ID: uuid.NewString(), Owner: s.Owner, Author: "bob", Recipient: "alice", Session: uuid.NewString(), Epoch: 1, CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour), Kind: "reply", Correlation: ask.ID, Body: "answer"}
	badAuthor := valid
	badAuthor.Author = "mallory"
	badRecipient := valid
	badRecipient.Recipient = "carol"
	missingAsk := valid
	missingAsk.Correlation = uuid.NewString()
	notAsk, e := s.Send(ctx, alice, "bob", "notice", "send", "", time.Hour, false)
	if e != nil {
		t.Fatal(e)
	}
	badKind := valid
	badKind.Correlation = notAsk.ID
	for _, m := range []record.Message{badAuthor, badRecipient, missingAsk, badKind} {
		if _, e = s.Receive(ctx, m); e == nil {
			t.Fatal("accepted unauthorized reply", m)
		}
		var count int
		if e = s.DB.QueryRow("SELECT count(*) FROM messages WHERE message_id=?", m.ID).Scan(&count); e != nil || count != 0 {
			t.Fatal("rejected reply persisted", count, e)
		}
	}
	if fresh, e := s.Receive(ctx, valid); e != nil || !fresh {
		t.Fatal(fresh, e)
	}
	if fresh, e := s.Receive(ctx, valid); e != nil || fresh {
		t.Fatal("valid duplicate reply", fresh, e)
	}
	if _, e = s.Send(ctx, bob, "", "local answer", "reply", ask.ID, time.Hour, false); !errors.Is(e, ErrAskResolved) {
		t.Fatal("second local answer after broker resolution was not rejected", e)
	}
}

func TestUpgradePreservesLegacyRowsJobsAndEventCursors(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "s")
	if e := identity.PrivateDir(dir); e != nil {
		t.Fatal(e)
	}
	// Construct the original MVP schema and a pending record, without reading live state.
	db, e := sql.Open("sqlite", filepath.Join(dir, "inbox.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec(`CREATE TABLE messages(id TEXT PRIMARY KEY,recipient TEXT NOT NULL,record TEXT NOT NULL,unread INTEGER NOT NULL DEFAULT 1);
 CREATE TABLE jobs(id TEXT NOT NULL REFERENCES messages(id),channel TEXT NOT NULL,epoch INTEGER NOT NULL,attempts INTEGER NOT NULL DEFAULT 0,next INTEGER NOT NULL DEFAULT 0,state TEXT NOT NULL DEFAULT 'pending',error TEXT NOT NULL DEFAULT '',PRIMARY KEY(id,channel));
 CREATE TABLE events(cursor INTEGER PRIMARY KEY AUTOINCREMENT,recipient TEXT NOT NULL,kind TEXT NOT NULL,message TEXT NOT NULL);`)
	if e != nil {
		t.Fatal(e)
	}
	m := record.Message{Type: record.MessageType, ID: uuid.NewString(), Owner: "did:key:test", Author: "alice", Recipient: "bob", Session: uuid.NewString(), Epoch: 1, CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour), Kind: "send", Body: "legacy"}
	raw, _ := json.Marshal(m)
	if _, e = db.Exec("INSERT INTO messages(id,recipient,record) VALUES(?,?,?)", m.ID, m.Recipient, string(raw)); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("INSERT INTO jobs(id,channel,epoch,attempts) VALUES(?,'native',1,3)", m.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("INSERT INTO events(cursor,recipient,kind,message) VALUES(42,?,'received',?)", m.Recipient, m.ID); e != nil {
		t.Fatal(e)
	}
	db.Close()
	// Match private-state permissions of the previous implementation.
	if e := os.Chmod(filepath.Join(dir, "inbox.sqlite"), 0600); e != nil {
		t.Fatal(e)
	}
	s, e := Open(dir, m.Owner)
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	if fresh, e := s.Receive(ctx, m); e != nil || fresh {
		t.Fatal("legacy duplicate not recognized", fresh, e)
	}
	jobs, e := s.Jobs(ctx, "native", time.Now())
	if e != nil || len(jobs) != 1 || jobs[0].Attempts != 3 {
		t.Fatal(jobs, e)
	}
	if e = s.Finish(ctx, jobs[0], "accepted", ""); e != nil {
		t.Fatal(e)
	}
	events, e := s.Events(ctx, "bob", 0)
	if e != nil || len(events) != 2 || events[0].Cursor != 42 || events[0].Origin != m.Origin() {
		t.Fatal(events, e)
	}
	s.Close()
	s, e = Open(dir, m.Owner)
	if e != nil {
		t.Fatal("migration is not idempotent", e)
	}
	defer s.Close()
	state, e := s.Status(ctx, "bob")
	if e != nil || state["native:accepted"] != 1 {
		t.Fatal(state, e)
	}
}
