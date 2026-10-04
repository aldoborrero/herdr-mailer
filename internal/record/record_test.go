package record

import (
	"encoding/json"
	"github.com/google/uuid"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUUIDAndMessageValidation(t *testing.T) {
	id := uuid.NewString()
	for _, invalid := range []string{"", "pane:p1", "urn:uuid:" + id, "{" + id + "}"} {
		if ValidUUID(invalid) {
			t.Fatal(invalid)
		}
	}
	m := Message{Type: MessageType, ID: id, Owner: "did:key:test", Author: "alice", Recipient: "bob", Session: uuid.NewString(), Epoch: 1, CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour), Kind: "send", Body: "body"}
	if e := m.Validate(); e != nil {
		t.Fatal(e)
	}
	cases := []func(*Message){func(m *Message) { m.Kind = "unknown" }, func(m *Message) { m.Recipient = "all.*" }, func(m *Message) { m.Epoch = 0 }, func(m *Message) { m.Body = "" }, func(m *Message) { m.ExpiresAt = m.CreatedAt }, func(m *Message) { m.Kind = "reply" }, func(m *Message) { m.Correlation = id }}
	for i, change := range cases {
		bad := m
		change(&bad)
		if bad.Validate() == nil {
			t.Fatal("accepted invalid message", i)
		}
	}
}

// Check that the published record declarations cover the emitted typed records.
// This is a schema/record consistency check, not a full ATProto validator.
func TestLexiconRecordConsistency(t *testing.T) {
	now := time.Now().UTC()
	records := []any{Message{Type: MessageType, ID: uuid.NewString(), Owner: "did:key:test", Author: "alice", Recipient: "bob", Session: uuid.NewString(), Epoch: 1, RecipientSession: uuid.NewString(), RecipientEpoch: 1, CreatedAt: now, ExpiresAt: now.Add(time.Hour), Kind: "reply", Correlation: uuid.NewString(), Body: "body"}, Session{Type: SessionType, Owner: "did:key:test", Agent: "alice", ID: uuid.NewString(), Epoch: 1, Runtime: "none", Location: "", CreatedAt: now}}
	for _, r := range records {
		raw, e := json.Marshal(r)
		if e != nil {
			t.Fatal(e)
		}
		var fields map[string]any
		if e = json.Unmarshal(raw, &fields); e != nil {
			t.Fatal(e)
		}
		kind := fields["$type"].(string)
		b, e := os.ReadFile(filepath.Join("..", "..", "lexicons", kind+".json"))
		if e != nil {
			t.Fatal(e)
		}
		var schema struct {
			Lexicon int    `json:"lexicon"`
			ID      string `json:"id"`
			Defs    map[string]struct {
				Type   string `json:"type"`
				Key    string `json:"key"`
				Record struct {
					Type       string   `json:"type"`
					Required   []string `json:"required"`
					Properties map[string]struct {
						Type      string `json:"type"`
						Format    string `json:"format"`
						MaxLength int    `json:"maxLength"`
					} `json:"properties"`
				} `json:"record"`
			} `json:"defs"`
		}
		if e = json.Unmarshal(b, &schema); e != nil {
			t.Fatal(e)
		}
		main := schema.Defs["main"]
		if schema.Lexicon != 1 || schema.ID != kind || main.Type != "record" || main.Key != "any" || main.Record.Type != "object" {
			t.Fatal("invalid record declaration")
		}
		for _, required := range main.Record.Required {
			if _, ok := fields[required]; !ok {
				t.Fatal("missing required field", required)
			}
		}
		for k, v := range fields {
			if k == "$type" {
				continue
			}
			prop, ok := main.Record.Properties[k]
			if !ok {
				t.Fatal("undeclared field", k)
			}
			if prop.Type == "string" {
				s, ok := v.(string)
				if !ok || prop.MaxLength > 0 && len(s) > prop.MaxLength {
					t.Fatal("invalid string", k)
				}
				if prop.Format == "datetime" {
					if _, e = time.Parse(time.RFC3339Nano, s); e != nil {
						t.Fatal(e)
					}
				}
			} else if prop.Type == "integer" {
				if v.(float64) < 1 {
					t.Fatal(k)
				}
			} else {
				t.Fatal("unexpected field type", prop.Type)
			}
		}
	}
}

func TestRecipientBindingMustBeComplete(t *testing.T) {
	m := Message{Type: MessageType, ID: uuid.NewString(), Owner: "did:key:test", Author: "alice", Recipient: "bob", Session: uuid.NewString(), Epoch: 1, CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour), Kind: "send", Body: "body"}
	m.RecipientSession = uuid.NewString()
	if m.Validate() == nil {
		t.Fatal("accepted target UUID without epoch")
	}
	m.RecipientSession = ""
	m.RecipientEpoch = 1
	if m.Validate() == nil {
		t.Fatal("accepted target epoch without UUID")
	}
	m.RecipientSession = uuid.NewString()
	if e := m.Validate(); e != nil {
		t.Fatal(e)
	}
}
