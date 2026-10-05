package daemon

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aldoborrero/herdr-mailer/internal/record"
)

func TestSocketIdempotentSendAndRecipientPrecondition(t *testing.T) {
	s, dir, a, _ := setup(t)
	cancel, done := start(t, s, dir)
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	ctx := context.Background()
	b, err := s.Store.Session(ctx, "bob")
	if err != nil {
		t.Fatal(err)
	}
	request := Request{Op: "send", Token: a, Recipient: "bob", Body: "one notice", OperationID: "stable-op", RecipientSession: b.ID, RecipientEpoch: b.Epoch}
	first, err := Call(ctx, dir, request, nil)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := Call(ctx, dir, request, nil)
	if err != nil || string(first) != string(replay) {
		t.Fatal("wire replay", err)
	}
	var m record.Message
	if err = json.Unmarshal(first, &m); err != nil {
		t.Fatal(err)
	}
	if m.RecipientSession != b.ID || m.RecipientEpoch != b.Epoch {
		t.Fatal("precondition lost")
	}
	if _, err = Call(ctx, dir, Request{Op: "register", Token: "admin", Agent: "bob", Runtime: "none"}, nil); err != nil {
		t.Fatal(err)
	}
	request.OperationID = "new-op"
	if _, err = Call(ctx, dir, request, nil); err == nil {
		t.Fatal("stale recipient accepted")
	}
	capabilities, err := Call(ctx, dir, Request{Op: "capabilities", Token: a}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err = json.Unmarshal(capabilities, &got); err != nil || got["idempotentSend"] != true || got["recipientPrecondition"] != true {
		t.Fatal(string(capabilities), err)
	}
}
