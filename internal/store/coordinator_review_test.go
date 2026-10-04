package store

import (
	"context"
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestCoordinatorRejectForgedBrokerReply(t *testing.T) {
	s, a, _ := fixture(t)
	ctx := context.Background()
	ask, err := s.Send(ctx, a, "bob", "question", "ask", "", time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	reply := ask
	reply.ID = uuid.NewString()
	reply.Kind = "reply"
	reply.Correlation = ask.ID
	reply.Author = "mallory"
	reply.Recipient = "alice"
	reply.Body = "forged response"
	if _, err = s.Receive(ctx, reply); err == nil {
		t.Fatal("accepted broker reply from non-recipient mallory")
	}
}
func TestCoordinatorRejectUnknownAskReply(t *testing.T) {
	s, a, _ := fixture(t)
	ctx := context.Background()
	m, err := s.Send(ctx, a, "bob", "seed", "send", "", time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	m.ID = uuid.NewString()
	m.Kind = "reply"
	m.Correlation = uuid.NewString()
	m.Author = "bob"
	m.Recipient = "alice"
	if _, err = s.Receive(ctx, m); err == nil {
		t.Fatal("accepted reply to nonexistent ask")
	}
}
func TestCoordinatorRejectSecondReply(t *testing.T) {
	s, a, b := fixture(t)
	ctx := context.Background()
	ask, err := s.Send(ctx, a, "bob", "question", "ask", "", time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Send(ctx, b, "", "first", "reply", ask.ID, time.Hour, false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Send(ctx, b, "", "second", "reply", ask.ID, time.Hour, false); err == nil {
		t.Fatal("accepted second answer to settled ask")
	}
}
