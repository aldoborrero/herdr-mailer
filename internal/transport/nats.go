// Package transport bridges a private trusted-daemon JetStream namespace.
package transport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/aldoborrero/herdr-mailer/internal/record"
	"github.com/nats-io/nats.go"
)

type Config struct {
	URL         string
	Stream      string
	Prefix      string
	Credentials string
}
type Bridge struct {
	conn   *nats.Conn
	js     nats.JetStreamContext
	config Config
}

var name = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func (c Config) Validate() error {
	if !name.MatchString(c.Stream) || !name.MatchString(c.Prefix) {
		return fmt.Errorf("stream and prefix must be single safe tokens")
	}
	if c.URL == "" {
		return fmt.Errorf("an explicit private NATS URL is required")
	}
	return nil
}

func Connect(c Config) (*Bridge, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	opts := []nats.Option{nats.Name("herdr-mailer"), nats.Timeout(time.Second), nats.MaxReconnects(-1), nats.ReconnectWait(time.Second)}
	if c.Credentials != "" {
		opts = append(opts, nats.UserCredentials(c.Credentials))
	}
	nc, err := nats.Connect(c.URL, opts...)
	if err != nil {
		return nil, err
	}
	js, err := nc.JetStream(nats.MaxWait(time.Second))
	if err != nil {
		nc.Close()
		return nil, err
	}
	info, err := js.StreamInfo(c.Stream)
	if err == nats.ErrStreamNotFound {
		info, err = js.AddStream(&nats.StreamConfig{Name: c.Stream, Subjects: []string{c.Prefix + ".*.*"}, Retention: nats.LimitsPolicy, Storage: nats.FileStorage, Discard: nats.DiscardNew, MaxAge: 0, MaxMsgs: -1, MaxBytes: -1, Duplicates: 2 * time.Minute})
	}
	if err != nil {
		nc.Close()
		return nil, err
	}
	sc := info.Config
	if sc.Retention != nats.LimitsPolicy || sc.Storage != nats.FileStorage || sc.MaxAge != 0 || sc.Discard != nats.DiscardNew || (sc.MaxMsgsPerSubject > 0 && !sc.DiscardNewPerSubject) || len(sc.Subjects) != 1 || sc.Subjects[0] != c.Prefix+".*.*" {
		nc.Close()
		return nil, fmt.Errorf("stream requires file storage, limits retention, no max age, discard-new (including per-subject limits) and exact private subjects")
	}
	return &Bridge{nc, js, c}, nil
}
func (b *Bridge) Close()          { b.conn.Close() }
func (b *Bridge) Connected() bool { return b.conn.IsConnected() }
func (b *Bridge) Publish(ctx context.Context, m record.Message) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	msg := nats.NewMsg(b.config.Prefix + "." + m.Author + "." + m.Recipient)
	msg.Data = data
	msg.Header.Set(nats.MsgIdHdr, m.ID)
	_, err = b.js.PublishMsg(msg, nats.Context(ctx))
	return err
}

type Receiver func(context.Context, record.Message) (bool, error)

// Handle binds envelope identities to the ACL-protected subject, persists, then ACKs.
func Handle(ctx context.Context, subject, prefix, recipient string, data []byte, persist Receiver, ack func() error) error {
	parts := strings.Split(subject, ".")
	var m record.Message
	if len(parts) != 3 || parts[0] != prefix || parts[2] != recipient {
		return fmt.Errorf("invalid subject")
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	if err := m.Validate(); err != nil {
		return err
	}
	if m.Author != parts[1] || m.Recipient != recipient {
		return fmt.Errorf("subject identity mismatch")
	}
	if _, err := persist(ctx, m); err != nil {
		return err
	}
	return ack()
}
func (b *Bridge) Subscribe(recipient string) (*nats.Subscription, error) {
	if !record.ValidAgent(recipient) {
		return nil, fmt.Errorf("invalid recipient")
	}
	h := sha256.Sum256([]byte(b.config.Prefix + ":" + recipient))
	durable := "hm_" + hex.EncodeToString(h[:12])
	return b.js.PullSubscribe(b.config.Prefix+".*."+recipient, durable, nats.BindStream(b.config.Stream), nats.ManualAck(), nats.AckExplicit(), nats.DeliverAll(), nats.AckWait(30*time.Second), nats.MaxAckPending(64))
}
func (b *Bridge) Consume(ctx context.Context, sub *nats.Subscription, recipient string, persist Receiver) error {
	return b.consume(ctx, ctx, sub, recipient, persist)
}

// Poll bounds an idle fetch without imposing that short deadline on disk persistence.
func (b *Bridge) Poll(ctx context.Context, sub *nats.Subscription, recipient string, persist Receiver) error {
	fetchCtx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	return b.consume(ctx, fetchCtx, sub, recipient, persist)
}

func (b *Bridge) consume(ctx, fetchCtx context.Context, sub *nats.Subscription, recipient string, persist Receiver) error {
	messages, err := sub.Fetch(32, nats.Context(fetchCtx))
	if err != nil {
		return err
	}
	for _, m := range messages {
		if err := Handle(ctx, m.Subject, b.config.Prefix, recipient, m.Data, persist, func() error {
			ackCtx, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			return m.AckSync(nats.Context(ackCtx))
		}); err != nil {
			_ = m.NakWithDelay(time.Second)
			return err
		}
	}
	return nil
}
