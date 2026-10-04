package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/aldoborrero/herdr-mailer/internal/delivery"
	"github.com/aldoborrero/herdr-mailer/internal/record"
	"github.com/aldoborrero/herdr-mailer/internal/store"
	"github.com/google/uuid"
)

var errRuntimeOffline = errors.New("runtime bridge disconnected; retained for reconnect")
var errChannelWritten = errors.New("channel notification written; runtime receipt unavailable")

type runtimeAck struct {
	Attempt string `json:"attempt"`
	Status  string `json:"status"`
}
type runtimePeer struct {
	conn    net.Conn
	session record.Session
	acks    chan runtimeAck
	done    chan struct{}
}

// Runtime connections use a dedicated bidirectional protocol, not inbox events.
// Only daemon delivery jobs produce frames; status/read/reply events cannot loop.
func (s *Server) runtimeConnect(ctx context.Context, conn net.Conn, dec *json.Decoder, r Request) {
	s.route.Lock()
	session, err := s.Store.Authenticate(ctx, r.Token)
	if err == nil && (session.Runtime != r.Runtime || session.Location != r.Location || (r.Runtime != "pi" && r.Runtime != "claude")) {
		err = store.ErrUnauthorized
	}
	s.runtimeMu.Lock()
	if s.peers == nil {
		s.peers = make(map[string]*runtimePeer)
	}
	if err == nil && s.peers[session.ID] != nil {
		err = fmt.Errorf("runtime session already connected")
	}
	if err != nil {
		s.runtimeMu.Unlock()
		s.route.Unlock()
		s.write(conn, Response{Error: err.Error()})
		return
	}
	p := &runtimePeer{conn: conn, session: session, acks: make(chan runtimeAck, 1), done: make(chan struct{})}
	s.peers[session.ID] = p
	s.runtimeMu.Unlock()
	ready := s.write(conn, Response{Data: map[string]any{"type": "ready", "session": session}})
	if ready {
		_, _ = s.Store.DB.ExecContext(ctx, "UPDATE jobs SET next=0 WHERE channel='native' AND state='pending' AND epoch=? AND id IN (SELECT id FROM messages WHERE recipient=?)", session.Epoch, session.Agent)
	}
	s.route.Unlock()
	defer func() {
		s.runtimeMu.Lock()
		if s.peers[session.ID] == p {
			delete(s.peers, session.ID)
		}
		close(p.done)
		s.runtimeMu.Unlock()
	}()
	if !ready {
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	// ACK frames are small and must be newline delimited; no unbounded Decode.
	scanner := bufio.NewScanner(io.MultiReader(dec.Buffered(), conn))
	scanner.Buffer(make([]byte, 1024), 4096)
	for scanner.Scan() {
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		var ack runtimeAck
		if json.Unmarshal(scanner.Bytes(), &ack) != nil || !record.ValidUUID(ack.Attempt) {
			return
		}
		select {
		case p.acks <- ack:
		case <-ctx.Done():
			return
		default:
			return
		}
	}
}

func (s *Server) disconnectRuntime(agent string) {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	for _, p := range s.peers {
		if p.session.Agent == agent {
			p.conn.Close()
		}
	}
}

// Caller holds route across the last epoch check, injection and receipt, so
// registration cannot rotate a destination while an injection is in flight.
func (s *Server) deliverRuntime(ctx context.Context, session record.Session, m record.Message) error {
	s.runtimeMu.Lock()
	p := s.peers[session.ID]
	s.runtimeMu.Unlock()
	if p == nil {
		return errRuntimeOffline
	}
	deadline := time.Now().Add(2 * time.Second)
	if m.ExpiresAt.Before(deadline) {
		deadline = m.ExpiresAt
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	attempt := uuid.NewString()
	// Hold the routing fence until the injection lease expires even on disconnect.
	// The bridge checks this lease immediately before calling the runtime API.
	waitLease := func() error {
		timer := time.NewTimer(time.Until(deadline))
		defer timer.Stop()
		<-timer.C
		return errRuntimeOffline
	}
	if !s.write(p.conn, Response{Data: map[string]any{"type": "delivery", "attempt": attempt, "deadline": deadline.UTC(), "session": session, "message": m}}) {
		p.conn.Close()
		return waitLease()
	}
	select {
	case ack := <-p.acks:
		if ack.Attempt != attempt {
			p.conn.Close()
			return waitLease()
		}
		switch ack.Status {
		case "accepted":
			if session.Runtime == "pi" {
				return nil
			}
		case "channel_written":
			if session.Runtime == "claude" {
				return errChannelWritten
			}
		case "ambiguous":
			return delivery.ErrAmbiguous
		}
		p.conn.Close()
		return waitLease()
	case <-p.done:
		return waitLease()
	case <-ctx.Done():
		p.conn.Close()
		return waitLease()
	}
}
