package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/aldoborrero/herdr-mailer/internal/record"
	"github.com/google/uuid"
)

func replyFor(ask record.Message) record.Message {
	return record.Message{Type: record.MessageType, ID: uuid.NewString(), Owner: ask.Owner, Author: ask.Recipient, Recipient: ask.Author, Session: uuid.NewString(), Epoch: 1, CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour), Kind: "reply", Correlation: ask.ID, Body: "answer"}
}
func counts(t *testing.T, s *Store) [4]int {
	t.Helper()
	var c [4]int
	if e := s.DB.QueryRow(`SELECT (SELECT count(*) FROM messages),(SELECT count(*) FROM jobs),(SELECT count(*) FROM events),(SELECT count(*) FROM ask_resolutions)`).Scan(&c[0], &c[1], &c[2], &c[3]); e != nil {
		t.Fatal(e)
	}
	return c
}
func TestReplyRejectsInvalidRoutesWithoutPartialState(t *testing.T) {
	s, a, _ := fixture(t)
	ctx := context.Background()
	ask, e := s.Send(ctx, a, "bob", "question", "ask", "", time.Hour, false)
	if e != nil {
		t.Fatal(e)
	}
	notice, e := s.Send(ctx, a, "bob", "notice", "send", "", time.Hour, false)
	if e != nil {
		t.Fatal(e)
	}
	before := counts(t, s)
	cases := map[string]func(*record.Message){"wrong author": func(m *record.Message) { m.Author = "mallory" }, "wrong recipient": func(m *record.Message) { m.Recipient = "mallory" }, "missing ask": func(m *record.Message) { m.Correlation = uuid.NewString() }, "not ask": func(m *record.Message) { m.Correlation = notice.ID }}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			m := replyFor(ask)
			change(&m)
			if _, e := s.Receive(ctx, m); e == nil {
				t.Fatal("invalid reply accepted")
			}
			if after := counts(t, s); after != before {
				t.Fatal("partial state", before, after)
			}
		})
	}
}
func TestSingleResolutionAndExactReplay(t *testing.T) {
	s, a, b := fixture(t)
	ctx := context.Background()
	ask, e := s.Send(ctx, a, "bob", "question", "ask", "", time.Hour, true)
	if e != nil {
		t.Fatal(e)
	}
	reply, e := s.Send(ctx, b, "", "first", "reply", ask.ID, time.Hour, true)
	if e != nil {
		t.Fatal(e)
	}
	settled := counts(t, s)
	if settled != [4]int{2, 4, 2, 1} {
		t.Fatal(settled)
	}
	if fresh, e := s.Receive(ctx, reply); e != nil || fresh {
		t.Fatal("legitimate replay rejected", fresh, e)
	}
	changed := reply
	changed.Body = "same ID, changed content"
	if _, e = s.Receive(ctx, changed); e == nil {
		t.Fatal("changed replay accepted")
	}
	second := reply
	second.ID = uuid.NewString()
	if _, e = s.Receive(ctx, second); !errors.Is(e, ErrAskResolved) {
		t.Fatal("second broker answer accepted", e)
	}
	if _, e = s.Send(ctx, b, "", "second local", "reply", ask.ID, time.Hour, true); !errors.Is(e, ErrAskResolved) {
		t.Fatal("second local answer accepted", e)
	}
	if after := counts(t, s); after != settled {
		t.Fatal("reply/replay left partial state", settled, after)
	}
}
func TestResolutionRollsBackWithJobsAndEvents(t *testing.T) {
	for _, table := range []string{"jobs", "events"} {
		t.Run(table, func(t *testing.T) {
			s, a, b := fixture(t)
			ctx := context.Background()
			ask, e := s.Send(ctx, a, "bob", "question", "ask", "", time.Hour, false)
			if e != nil {
				t.Fatal(e)
			}
			before := counts(t, s)
			if _, e = s.DB.Exec("CREATE TRIGGER fail_reply BEFORE INSERT ON " + table + " BEGIN SELECT RAISE(ABORT,'simulated write failure'); END"); e != nil {
				t.Fatal(e)
			}
			reply, e := s.Send(ctx, b, "", "retryable", "reply", ask.ID, time.Hour, true)
			if e == nil {
				t.Fatal("expected write failure")
			}
			if after := counts(t, s); after != before {
				t.Fatal("partial transaction", before, after)
			}
			if _, e = s.DB.Exec("DROP TRIGGER fail_reply"); e != nil {
				t.Fatal(e)
			}
			if fresh, e := s.Receive(ctx, reply); e != nil || !fresh {
				t.Fatal("rollback left a phantom resolution", fresh, e)
			}
		})
	}
}
func TestConcurrentRepliesChooseOneResolution(t *testing.T) {
	for _, mode := range []string{"local", "broker", "mixed"} {
		t.Run(mode, func(t *testing.T) {
			s, a, b := fixture(t)
			ctx := context.Background()
			ask, e := s.Send(ctx, a, "bob", "question", "ask", "", time.Hour, false)
			if e != nil {
				t.Fatal(e)
			}
			start := make(chan struct{})
			results := make(chan error, 16)
			for i := 0; i < 16; i++ {
				go func(i int) {
					<-start
					var e error
					if mode == "local" || mode == "mixed" && i%2 == 0 {
						_, e = s.Send(ctx, b, "", "competing answer", "reply", ask.ID, time.Hour, false)
					} else {
						_, e = s.Receive(ctx, replyFor(ask))
					}
					results <- e
				}(i)
			}
			close(start)
			winners := 0
			for i := 0; i < 16; i++ {
				e := <-results
				if e == nil {
					winners++
				} else if !errors.Is(e, ErrAskResolved) {
					t.Fatal(e)
				}
			}
			if winners != 1 {
				t.Fatal("expected one resolution", winners)
			}
			if got := counts(t, s); got != [4]int{2, 2, 2, 1} {
				t.Fatal("concurrent partial state", got)
			}
		})
	}
}
func TestConcurrentReplayIsIdempotent(t *testing.T) {
	s, a, _ := fixture(t)
	ctx := context.Background()
	ask, e := s.Send(ctx, a, "bob", "question", "ask", "", time.Hour, false)
	if e != nil {
		t.Fatal(e)
	}
	reply := replyFor(ask)
	start := make(chan struct{})
	type result struct {
		fresh bool
		err   error
	}
	results := make(chan result, 16)
	for i := 0; i < 16; i++ {
		go func() { <-start; fresh, e := s.Receive(ctx, reply); results <- result{fresh, e} }()
	}
	close(start)
	freshCount := 0
	for i := 0; i < 16; i++ {
		r := <-results
		if r.err != nil {
			t.Fatal(r.err)
		}
		if r.fresh {
			freshCount++
		}
	}
	if freshCount != 1 {
		t.Fatal(freshCount)
	}
	if got := counts(t, s); got != [4]int{2, 2, 2, 1} {
		t.Fatal(got)
	}
}
func TestResolutionSurvivesRestartAndUpgrade(t *testing.T) {
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
	_, _, e = s.Register(ctx, "bob", "none", "")
	if e != nil {
		t.Fatal(e)
	}
	ask, e := s.Send(ctx, a, "bob", "question", "ask", "", time.Hour, false)
	if e != nil {
		t.Fatal(e)
	}
	reply := replyFor(ask)
	if _, e = s.Receive(ctx, reply); e != nil {
		t.Fatal(e)
	}
	// Simulate the immediately previous schema, which had no resolution registry.
	if _, e = s.DB.Exec("DROP TABLE ask_resolutions; DELETE FROM metadata WHERE key='reply_resolution_schema'"); e != nil {
		t.Fatal(e)
	}
	s.Close()
	for i := 0; i < 2; i++ {
		s, e = Open(dir, ask.Owner)
		if e != nil {
			t.Fatal(e)
		}
		if fresh, e := s.Receive(ctx, reply); e != nil || fresh {
			t.Fatal(fresh, e)
		}
		if _, e = s.Receive(ctx, replyFor(ask)); !errors.Is(e, ErrAskResolved) {
			t.Fatal(e)
		}
		if got := counts(t, s); got != [4]int{2, 2, 2, 1} {
			t.Fatal(got)
		}
		s.Close()
	}
}
func TestUpgradeRejectsConflictingLegacyReplies(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "s")
	s, e := Open(dir, "did:key:test")
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	_, a, e := s.Register(ctx, "alice", "none", "")
	if e != nil {
		t.Fatal(e)
	}
	ask, e := s.Send(ctx, a, "bob", "question", "ask", "", time.Hour, false)
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		reply := replyFor(ask)
		raw, _ := json.Marshal(reply)
		if _, e = s.DB.Exec("INSERT INTO messages(id,origin,message_id,recipient,record) VALUES(?,?,?,?,?)", reply.DeliveryID(), reply.Origin(), reply.ID, reply.Recipient, string(raw)); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = s.DB.Exec("DROP TABLE ask_resolutions; DELETE FROM metadata WHERE key='reply_resolution_schema'"); e != nil {
		t.Fatal(e)
	}
	s.Close()
	if reopened, e := Open(dir, ask.Owner); e == nil {
		reopened.Close()
		t.Fatal("upgrade silently chose a winner for conflicting history")
	}
	db, e := sql.Open("sqlite", filepath.Join(dir, "inbox.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var count int
	if e = db.QueryRow("SELECT count(*) FROM messages").Scan(&count); e != nil || count != 3 {
		t.Fatal("upgrade destroyed evidence", count, e)
	}
	if e = db.QueryRow("SELECT count(*) FROM metadata WHERE key='reply_resolution_schema'").Scan(&count); e != nil || count != 0 {
		t.Fatal("partial migration", count, e)
	}
}

func TestResolutionsScopedToAskOrigin(t *testing.T) {
	s, a, b := fixture(t)
	ctx := context.Background()
	ask, e := s.Send(ctx, a, "bob", "alice question", "ask", "", time.Hour, false)
	if e != nil {
		t.Fatal(e)
	}
	other := ask
	other.Author = "carol"
	other.Body = "carol question"
	if _, e = s.Receive(ctx, other); e != nil {
		t.Fatal(e)
	}
	for _, author := range []string{"alice", "carol"} {
		if _, e = s.Send(ctx, b, "", "answer", "reply", ask.ID, time.Hour, false, author); e != nil {
			t.Fatal("different ask origin conflated", author, e)
		}
	}
	if got := counts(t, s); got != [4]int{4, 4, 4, 2} {
		t.Fatal(got)
	}
}
