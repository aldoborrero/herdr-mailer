package daemon

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aldoborrero/herdr-mailer/internal/record"
)

func TestRequestBodyByteLimit(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"six-byte-escapes", strings.Repeat("\x00", record.MaxBodyBytes), true},
		{"multibyte-UTF8", strings.Repeat("é", record.MaxBodyBytes/2), true},
		{"ASCII-over-limit", strings.Repeat("a", record.MaxBodyBytes+1), false},
		{"escaped-over-limit", strings.Repeat("\n", record.MaxBodyBytes+1), false},
		{"multibyte-over-limit", strings.Repeat("é", record.MaxBodyBytes/2+1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, dir, token, _ := setup(t)
			cancel, done := start(t, s, dir)
			defer func() { cancel(); <-done }()
			_, err := Call(context.Background(), dir, Request{Op: "send", Token: token, Recipient: "bob", Body: tc.body}, nil)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
			messages, err := s.Store.Inbox(context.Background(), "bob", false)
			if err != nil {
				t.Fatal(err)
			}
			if tc.valid {
				if len(messages) != 1 || messages[0].Body != tc.body {
					t.Fatal("body was not preserved")
				}
			} else {
				assertNoRequestWrites(t, s)
			}
		})
	}
}

func assertNoRequestWrites(t *testing.T, s *Server) {
	t.Helper()
	for _, table := range []string{"messages", "jobs", "events", "ask_resolutions"} {
		var count int
		if err := s.Store.DB.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("rejected request left %d rows in %s", count, table)
		}
	}
}

func TestRequestDocumentLimit(t *testing.T) {
	for _, name := range []string{"exact-limit", "one-byte-over", "huge-metadata", "huge-whitespace"} {
		t.Run(name, func(t *testing.T) {
			s, dir, token, _ := setup(t)
			cancel, done := start(t, s, dir)
			defer func() { cancel(); <-done }()
			request := Request{Op: "send", Token: token, Recipient: "bob", Body: "valid"}
			if name == "huge-metadata" {
				request.Location = strings.Repeat("x", 2*maxRequestBytes)
			}
			raw, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			switch name {
			case "exact-limit":
				raw = append([]byte(strings.Repeat(" ", maxRequestBytes-len(raw))), raw...)
			case "one-byte-over":
				raw = append([]byte(strings.Repeat(" ", maxRequestBytes+1-len(raw))), raw...)
			case "huge-whitespace":
				raw = append([]byte(strings.Repeat(" ", 2*maxRequestBytes)), raw...)
			}
			conn, err := net.Dial("unix", filepath.Join(dir, "daemon.sock"))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if err = conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			written := make(chan struct{})
			go func() { defer close(written); _, _ = conn.Write(raw) }()
			var response Response
			err = json.NewDecoder(conn).Decode(&response)
			conn.Close()
			<-written
			if err != nil {
				t.Fatal(err)
			}
			if name == "exact-limit" {
				if response.Error != "" {
					t.Fatal(response.Error)
				}
				messages, err := s.Store.Inbox(context.Background(), "bob", false)
				if err != nil || len(messages) != 1 {
					t.Fatalf("messages=%v error=%v", messages, err)
				}
			} else {
				if !strings.Contains(response.Error, "request document exceeds") {
					t.Fatalf("unexpected response: %+v", response)
				}
				assertNoRequestWrites(t, s)
			}
		})
	}
}
