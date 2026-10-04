package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCLICompatibilityFlow(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	ctx := context.Background()
	run := func(args ...string) []byte {
		t.Helper()
		var out bytes.Buffer
		if e := Run(ctx, append([]string{"--state", dir}, args...), &out, &out); e != nil {
			t.Fatalf("%v: %v %s", args, e, out.String())
		}
		return out.Bytes()
	}
	run("init")
	daemonCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- Run(daemonCtx, []string{"--state", dir, "daemon"}, &bytes.Buffer{}, &bytes.Buffer{}) }()
	defer func() {
		cancel()
		select {
		case e := <-done:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(time.Second):
			t.Error("daemon shutdown")
		}
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, e := os.Stat(filepath.Join(dir, "daemon.sock")); e == nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	a, b := filepath.Join(dir, "alice.token"), filepath.Join(dir, "bob.token")
	run("register", "alice", "--output", a)
	run("register", "bob", "--output", b)
	var ask struct {
		ID string `json:"id"`
	}
	if e := json.Unmarshal(run("--credential", a, "ask", "bob", "question"), &ask); e != nil {
		t.Fatal(e)
	}
	run("--credential", a, "send", "bob", "notice")
	var inbox []map[string]any
	if e := json.Unmarshal(run("--credential", b, "inbox", "--unread", "--json"), &inbox); e != nil || len(inbox) != 2 {
		t.Fatal(inbox, e)
	}
	run("--credential", b, "reply", ask.ID, "answer", "--author", "alice")
	run("--credential", b, "read", ask.ID, "--author", "alice")
	run("--credential", b, "status")
	run("--credential", b, "doctor")
	run("--credential", b, "events", "--cursor", "0")
}
