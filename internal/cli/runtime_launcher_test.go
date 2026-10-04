package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/aldoborrero/herdr-mailer/internal/record"
	"github.com/google/uuid"
)

// The subprocess runs the same CLI entry used by cmd/herdr-mailer. Its only
// arguments are reply/id/body: state and credential must come from the runtime.
func TestRuntimeCLIProcess(t *testing.T) {
	if os.Getenv("HERDR_MAILER_TEST_CLI") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			if err := Run(context.Background(), os.Args[i+1:], os.Stdout, os.Stderr); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			os.Exit(0)
		}
	}
	os.Exit(2)
}

func TestRuntimeLauncherReplyUsesReceiverEnvironment(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("node required for native launcher integration tests")
	}
	launcher, err := filepath.Abs("../../runtimes/launch.js")
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, runtime := range []string{"pi", "claude"} {
		t.Run(runtime, func(t *testing.T) {
			dir := t.TempDir()
			state := filepath.Join(dir, "receiver-state")
			run := func(args ...string) []byte {
				t.Helper()
				var out bytes.Buffer
				if err := Run(context.Background(), append([]string{"--state", state}, args...), &out, &out); err != nil {
					t.Fatalf("CLI: %v", err)
				}
				return out.Bytes()
			}
			run("init")
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- Run(ctx, []string{"--state", state, "daemon"}, &bytes.Buffer{}, &bytes.Buffer{}) }()
			defer func() {
				cancel()
				select {
				case err := <-done:
					if err != nil {
						t.Error(err)
					}
				case <-time.After(3 * time.Second):
					t.Error("daemon did not stop")
				}
			}()
			deadline := time.Now().Add(3 * time.Second)
			for {
				if _, err := os.Stat(filepath.Join(state, "daemon.sock")); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("daemon not ready")
				}
				time.Sleep(time.Millisecond)
			}
			sender := filepath.Join(dir, "sender.token")
			receiver := filepath.Join(dir, "receiver.token")
			run("register", "sender", "--runtime", "none", "--output", sender)
			location := uuid.NewString()
			run("register", "receiver", "--runtime", runtime, "--location", location, "--output", receiver)
			var ask record.Message
			if err := json.Unmarshal(run("--credential", sender, "ask", "receiver", "question"), &ask); err != nil {
				t.Fatal(err)
			}
			config := filepath.Join(dir, "config.json")
			raw, err := json.Marshal(map[string]string{"runtime": runtime, "location": location, "state": state, "credential": receiver, "receipts": filepath.Join(dir, "receipts")})
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(config, raw, 0600); err != nil {
				t.Fatal(err)
			}
			// Both fake runtimes invoke the CLI with their inherited environment. No
			// sender/credential/state flags are supplied to repair that environment.
			script := `#!` + node + `
const {spawnSync}=require('node:child_process');
const result=spawnSync(process.env.HERDR_MAILER_TEST_EXECUTABLE,
 ['-test.run=^TestRuntimeCLIProcess$','--','reply',process.env.HERDR_MAILER_TEST_ASK,'answer from runtime'],
 {encoding:'utf8',env:process.env});
process.stdout.write(result.stdout || '');
process.stderr.write(result.stderr || '');
process.exit(result.status ?? 1);
`
			if err = os.WriteFile(filepath.Join(dir, runtime), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("HERDR_MAILER_CONFIG", config)
			t.Setenv("HERDR_MAILER_CREDENTIAL", sender)
			t.Setenv("HERDR_MAILER_STATE", filepath.Join(dir, "wrong-sender-state"))
			t.Setenv("HERDR_MAILER_TEST_CLI", "1")
			t.Setenv("HERDR_MAILER_TEST_EXECUTABLE", executable)
			t.Setenv("HERDR_MAILER_TEST_ASK", ask.ID)
			args := []string{launcher}
			if runtime == "claude" {
				args = append(args, "--development-channel")
			}
			commandCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
			defer stop()
			command := exec.CommandContext(commandCtx, node, args...)
			var stdout, stderr bytes.Buffer
			command.Stdout = &stdout
			command.Stderr = &stderr
			if err = command.Run(); err != nil {
				t.Fatalf("runtime reply failed: %v: %s", err, stderr.String())
			}
			var reply record.Message
			if err = json.Unmarshal(stdout.Bytes(), &reply); err != nil {
				t.Fatal(err)
			}
			if reply.Author != "receiver" || reply.Recipient != "sender" || reply.Correlation != ask.ID || reply.Kind != "reply" {
				t.Fatalf("wrong reply attribution: %+v", reply)
			}
			var inbox []record.Message
			if err = json.Unmarshal(run("--credential", sender, "inbox"), &inbox); err != nil {
				t.Fatal(err)
			}
			if len(inbox) != 1 || inbox[0].ID != reply.ID {
				t.Fatal("reply not persisted in original sender inbox")
			}
			for _, file := range []string{sender, receiver} {
				token, err := os.ReadFile(file)
				if err != nil {
					t.Fatal(err)
				}
				token = bytes.TrimSpace(token)
				if bytes.Contains(stdout.Bytes(), token) || bytes.Contains(stderr.Bytes(), token) {
					t.Fatal("token leaked to runtime output")
				}
			}
		})
	}
}
