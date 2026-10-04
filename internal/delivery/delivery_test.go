package delivery

import (
	"context"
	"errors"
	"github.com/aldoborrero/herdr-mailer/internal/record"
	"reflect"
	"testing"
	"time"
)

func TestCodexArgumentsAndFailure(t *testing.T) {
	s := record.Session{Runtime: "codex", Location: "01a0f9be-cb70-7390-bcad-7c50b52940be"}
	m := record.Message{ID: "id", Kind: "ask", Author: "alice", Body: "$(touch /tmp/do-not-run); `shell`\n--enable anything", ExpiresAt: time.Now().Add(time.Hour)}
	failure := errors.New("exit 7")
	calls := 0
	c := Codex{Executable: "/private/codex", Run: func(ctx context.Context, exe string, args ...string) error {
		calls++
		if exe != "/private/codex" || !reflect.DeepEqual(args, []string{"queue", "--thread", s.Location, "--message", "[herdr-mailer id; ask from alice; correlation=]\n" + m.Body}) {
			t.Fatal(exe, args)
		}
		return failure
	}}
	if e := c.Deliver(context.Background(), s, m); !errors.Is(e, failure) {
		t.Fatal(e)
	}
	s.Runtime = "pi"
	if e := c.Deliver(context.Background(), s, m); !errors.Is(e, ErrUnsupported) {
		t.Fatal(e)
	}
	s.Runtime = "codex"
	s.Location = "pane-name"
	if e := c.Deliver(context.Background(), s, m); e == nil {
		t.Fatal("accepted non-UUID")
	}
	if calls != 1 {
		t.Fatal(calls)
	}
}
func TestTimeoutAmbiguousAndExpiredNoInvocation(t *testing.T) {
	s := record.Session{Runtime: "codex", Location: "01a0f9be-cb70-7390-bcad-7c50b52940be"}
	calls := 0
	c := Codex{Run: func(ctx context.Context, _ string, _ ...string) error { calls++; <-ctx.Done(); return ctx.Err() }}
	m := record.Message{ExpiresAt: time.Now().Add(20 * time.Millisecond)}
	if e := c.Deliver(context.Background(), s, m); !errors.Is(e, ErrAmbiguous) {
		t.Fatal(e)
	}
	if e := c.Deliver(context.Background(), s, m); e == nil {
		t.Fatal("expired")
	}
	if calls != 1 {
		t.Fatal(calls)
	}
}
