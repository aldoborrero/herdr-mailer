package daemon

import (
	"context"
	"github.com/aldoborrero/herdr-mailer/internal/delivery"
	"github.com/aldoborrero/herdr-mailer/internal/testutil"
	"github.com/aldoborrero/herdr-mailer/internal/transport"
	"testing"
	"time"
)

func TestBrokerOutboxSurvivesOutage(t *testing.T) {
	broker := testutil.NewBroker(t)
	s, dir, a, _ := setup(t)
	s.Broker = &transport.Config{URL: broker.URL, Stream: "MAILER", Prefix: "mailer"}
	broker.Stop()
	ctx := context.Background()
	m, e := s.Store.Send(ctx, a, "bob", "offline broker", "send", "", time.Hour, true)
	if e != nil {
		t.Fatal(e)
	}
	cancel, done := start(t, s, dir)
	// Broker is down; authoritative message and broker outbox remain intact.
	time.Sleep(300 * time.Millisecond)
	jobs, e := s.Store.Jobs(ctx, "broker", time.Now().Add(time.Hour))
	if e != nil || len(jobs) != 1 || jobs[0].Message.ID != m.ID {
		t.Fatal(jobs, e)
	}
	broker.Start(t)
	deadline := time.Now().Add(8 * time.Second)
	published := false
	for time.Now().Before(deadline) {
		status, e := s.Store.Status(ctx, "bob")
		if e != nil {
			t.Fatal(e)
		}
		if status["broker:published"] == 1 {
			published = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !published {
		t.Fatal("outbox did not replay after broker reconnect")
	}
	inbox, e := s.Store.Inbox(ctx, "bob", false)
	if e != nil || len(inbox) != 1 {
		t.Fatal("loopback duplicate", inbox, e)
	}
	cancel()
	select {
	case e = <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown timed out")
	}
}

func TestBrokerAckDoesNotClaimNativeAcceptanceOrRead(t *testing.T) {
	broker := testutil.NewBroker(t)
	s, dir, a, _ := setup(t)
	s.Adapter.(*fakeAdapter).err = delivery.ErrUnsupported
	s.Broker = &transport.Config{URL: broker.URL, Stream: "SEPARATE", Prefix: "separate"}
	if _, e := s.Store.Send(context.Background(), a, "bob", "separate receipts", "send", "", time.Hour, true); e != nil {
		t.Fatal(e)
	}
	cancel, done := start(t, s, dir)
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		state, e := s.Store.Status(context.Background(), "bob")
		if e != nil {
			t.Fatal(e)
		}
		if state["broker:published"] == 1 {
			if state["native:accepted"] != 0 || state["native:unsupported"] != 1 {
				t.Fatal(state)
			}
			inbox, e := s.Store.Inbox(context.Background(), "bob", true)
			if e != nil || len(inbox) != 1 {
				t.Fatal("broker ACK changed unread state", inbox, e)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("broker publication did not finish")
}
