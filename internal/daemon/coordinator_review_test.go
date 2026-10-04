package daemon

import (
	"context"
	"strings"
	"testing"
)

func TestCoordinatorAcceptMaxEscapedBody(t *testing.T) {
	s, dir, a, _ := setup(t)
	cancel, done := start(t, s, dir)
	defer func() { cancel(); <-done }()
	_, err := Call(context.Background(), dir, Request{Op: "send", Token: a, Recipient: "bob", Body: strings.Repeat("\n", 65536)}, nil)
	if err != nil {
		t.Fatalf("documented valid 65536-byte UTF-8 body rejected: %v", err)
	}
}
