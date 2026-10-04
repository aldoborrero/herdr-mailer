package record

import (
	"fmt"
	"github.com/google/uuid"
	"time"
	"unicode/utf8"
)

const MessageType = "com.example.herdr.messageV1"
const SessionType = "com.example.herdr.sessionV1"
const MaxBodyBytes = 65536

type Message struct {
	Type             string    `json:"$type"`
	ID               string    `json:"id"`
	Owner            string    `json:"owner"`
	Author           string    `json:"author"`
	Recipient        string    `json:"recipient"`
	Session          string    `json:"session"`
	Epoch            int64     `json:"epoch"`
	RecipientSession string    `json:"recipientSession,omitempty"`
	RecipientEpoch   int64     `json:"recipientEpoch,omitempty"`
	CreatedAt        time.Time `json:"createdAt"`
	ExpiresAt        time.Time `json:"expiresAt"`
	Kind             string    `json:"kind"`
	Correlation      string    `json:"correlation,omitempty"`
	Body             string    `json:"body"`
}
type Session struct {
	Type      string    `json:"$type"`
	Owner     string    `json:"owner"`
	Agent     string    `json:"agent"`
	ID        string    `json:"id"`
	Epoch     int64     `json:"epoch"`
	Runtime   string    `json:"runtime"`
	Location  string    `json:"location"`
	CreatedAt time.Time `json:"createdAt"`
}

func (m Message) Validate() error {
	if m.Type != MessageType || !ValidUUID(m.ID) || !ValidUUID(m.Session) || m.Owner == "" || !ValidAgent(m.Author) || !ValidAgent(m.Recipient) || m.Epoch < 1 || m.Body == "" || len(m.Body) > MaxBodyBytes || !utf8.ValidString(m.Body) || m.CreatedAt.IsZero() || !m.ExpiresAt.After(m.CreatedAt) {
		return fmt.Errorf("invalid message record")
	}
	if m.Kind != "send" && m.Kind != "ask" && m.Kind != "reply" {
		return fmt.Errorf("invalid kind")
	}
	if (m.RecipientSession != "" || m.RecipientEpoch != 0) && (!ValidUUID(m.RecipientSession) || m.RecipientEpoch < 1) {
		return fmt.Errorf("recipient session and epoch must be a valid pair")
	}
	if m.Kind == "reply" && !ValidUUID(m.Correlation) {
		return fmt.Errorf("reply needs ask correlation")
	}
	if m.Kind != "reply" && m.Correlation != "" {
		return fmt.Errorf("unexpected correlation")
	}
	return nil
}

// Origin is a stable logical author within the configured trusted owner domain.
// Session rotation does not change the deduplication namespace.
func (m Message) Origin() string     { return m.Owner + "/" + m.Author }
func (m Message) DeliveryID() string { return m.Origin() + "/" + m.ID }

func ValidUUID(s string) bool {
	id, err := uuid.Parse(s)
	return err == nil && s == id.String()
}
func ValidAgent(s string) bool {
	if len(s) < 1 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
