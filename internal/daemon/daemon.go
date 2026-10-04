package daemon

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/aldoborrero/herdr-mailer/internal/delivery"
	"github.com/aldoborrero/herdr-mailer/internal/identity"
	"github.com/aldoborrero/herdr-mailer/internal/store"
	"github.com/aldoborrero/herdr-mailer/internal/transport"
	"github.com/nats-io/nats.go"
	"golang.org/x/sys/unix"
)

type Request struct {
	Op        string `json:"op"`
	Token     string `json:"token,omitempty"`
	Agent     string `json:"agent,omitempty"`
	Author    string `json:"author,omitempty"`
	Runtime   string `json:"runtime,omitempty"`
	Location  string `json:"location,omitempty"`
	Recipient string `json:"recipient,omitempty"`
	Body      string `json:"body,omitempty"`
	ID        string `json:"id,omitempty"`
	TTL       string `json:"ttl,omitempty"`
	Unread    bool   `json:"unread,omitempty"`
	Follow    bool   `json:"follow,omitempty"`
	Cursor    int64  `json:"cursor,omitempty"`
}
type Response struct {
	Data  any    `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}
type Server struct {
	Store         *store.Store
	Admin         string
	Adapter       delivery.Adapter
	Broker        *transport.Config
	route         sync.Mutex
	healthMu      sync.Mutex
	brokerState   string
	broker        *transport.Bridge
	subscriptions map[string]*nats.Subscription
}

func (s *Server) Serve(ctx context.Context, dir string) error {
	if s.Broker != nil {
		if err := s.Broker.Validate(); err != nil {
			return err
		}
	}
	if err := identity.PrivateDir(dir); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return fmt.Errorf("daemon already running: %w", err)
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	path := filepath.Join(dir, "daemon.sock")
	if err = os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(path)
	if err = os.Chmod(path, 0600); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	var mu sync.Mutex
	connections := map[net.Conn]bool{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-ctx.Done()
		listener.Close()
		mu.Lock()
		for c := range connections {
			c.Close()
		}
		mu.Unlock()
	}()
	wg.Add(1)
	go func() { defer wg.Done(); s.work(ctx) }()
	defer func() {
		cancel()
		wg.Wait()
		if s.broker != nil {
			s.broker.Close()
		}
	}()
	for {
		conn, e := listener.Accept()
		if e != nil {
			if ctx.Err() != nil {
				return nil
			}
			return e
		}
		mu.Lock()
		connections[conn] = true
		if ctx.Err() != nil {
			conn.Close()
		}
		mu.Unlock()
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer conn.Close()
			defer func() { mu.Lock(); delete(connections, conn); mu.Unlock() }()
			s.handle(ctx, conn)
		}()
	}
}
func (s *Server) handle(parent context.Context, conn net.Conn) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	dec := json.NewDecoder(io.LimitReader(conn, 128*1024))
	dec.DisallowUnknownFields()
	var r Request
	if err := dec.Decode(&r); err != nil {
		s.write(conn, Response{Error: err.Error()})
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	// EOF or any further data cancels a streaming request; only one request per connection.
	go func() { var b [1]byte; _, _ = conn.Read(b[:]); cancel() }()
	if r.Op == "events" {
		s.events(ctx, conn, r)
		return
	}
	data, err := s.dispatch(ctx, r)
	resp := Response{Data: data}
	if err != nil {
		resp = Response{Error: err.Error()}
	}
	s.write(conn, resp)
}
func (s *Server) write(conn net.Conn, r Response) bool {
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return json.NewEncoder(conn).Encode(r) == nil
}
func (s *Server) dispatch(ctx context.Context, r Request) (any, error) {
	if r.Author != "" && r.Op != "read" && r.Op != "reply" {
		return nil, fmt.Errorf("author selects an existing read/reply record; it cannot set the sender")
	}
	if r.Op == "register" {
		if subtle.ConstantTimeCompare([]byte(identity.Hash(r.Token)), []byte(identity.Hash(s.Admin))) != 1 {
			return nil, store.ErrUnauthorized
		}
		s.route.Lock()
		defer s.route.Unlock()
		session, token, err := s.Store.Register(ctx, r.Agent, r.Runtime, r.Location)
		return map[string]any{"session": session, "token": token}, err
	}
	session, err := s.Store.Authenticate(ctx, r.Token)
	if err != nil {
		return nil, err
	}
	switch r.Op {
	case "send", "ask", "reply":
		ttl := 24 * time.Hour
		if r.TTL != "" {
			ttl, err = time.ParseDuration(r.TTL)
			if err != nil {
				return nil, err
			}
		}
		return s.Store.Send(ctx, r.Token, r.Recipient, r.Body, r.Op, r.ID, ttl, s.Broker != nil, r.Author)
	case "inbox":
		return s.Store.Inbox(ctx, session.Agent, r.Unread)
	case "read":
		return s.Store.Read(ctx, session.Agent, r.ID, true, r.Author)
	case "status":
		return s.Store.Status(ctx, session.Agent)
	case "doctor":
		var result string
		err = s.Store.DB.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&result)
		s.healthMu.Lock()
		brokerState := s.brokerState
		s.healthMu.Unlock()
		return map[string]any{"sqlite": result, "owner": s.Store.Owner, "agent": session.Agent, "session": session.ID, "epoch": session.Epoch, "runtime": session.Runtime, "location": session.Location, "brokerConfigured": s.Broker != nil, "brokerState": brokerState, "nativeReceipt": "queue acceptance only; model processing unknown"}, err
	default:
		return nil, fmt.Errorf("unknown operation")
	}
}
func (s *Server) events(ctx context.Context, conn net.Conn, r Request) {
	for {
		changed := s.Store.Changed()
		session, err := s.Store.Authenticate(ctx, r.Token)
		if err != nil {
			s.write(conn, Response{Error: err.Error()})
			return
		}
		events, err := s.Store.Events(ctx, session.Agent, r.Cursor)
		if err != nil {
			s.write(conn, Response{Error: err.Error()})
			return
		}
		for _, e := range events {
			if !s.write(conn, Response{Data: e}) {
				return
			}
			r.Cursor = e.Cursor
		}
		if !r.Follow && len(events) < 100 {
			s.write(conn, Response{Data: map[string]bool{"done": true}})
			return
		}
		if len(events) > 0 {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-changed:
		}
	}
}
func (s *Server) work(ctx context.Context) {
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	retry := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if s.Broker != nil && s.broker == nil && time.Now().After(retry) {
			b, err := transport.Connect(*s.Broker)
			if err != nil {
				retry = time.Now().Add(2 * time.Second)
			} else {
				s.broker = b
				s.subscriptions = map[string]*nats.Subscription{}
			}
		}
		if err := s.ProcessNative(ctx); err != nil && ctx.Err() == nil {
			log.Printf("native worker: %v", err)
		}
		if s.broker != nil {
			s.processBroker(ctx)
		}
		s.healthMu.Lock()
		s.brokerState = "disabled"
		if s.Broker != nil {
			s.brokerState = "disconnected or unavailable"
			if s.broker != nil && s.broker.Connected() {
				s.brokerState = "connected"
			}
		}
		s.healthMu.Unlock()
	}
}

// ProcessNative serializes session changes with validation and injection, preventing epoch races.
func (s *Server) ProcessNative(ctx context.Context) error {
	jobs, err := s.Store.Jobs(ctx, "native", time.Now())
	if err != nil {
		return err
	}
	for _, j := range jobs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		s.route.Lock()
		err = s.deliver(ctx, j)
		s.route.Unlock()
		if err != nil {
			return err
		}
	}
	return nil
}
func (s *Server) deliver(ctx context.Context, j store.Job) error {
	finish := func(state string, err error) error {
		detail := ""
		if err != nil {
			detail = err.Error()
		}
		finishCtx := ctx
		if ctx.Err() != nil {
			// Preserve an observed queue outcome during graceful shutdown too.
			var cancel context.CancelFunc
			finishCtx, cancel = context.WithTimeout(context.WithoutCancel(ctx), time.Second)
			defer cancel()
		}
		return s.Store.Finish(finishCtx, j, state, detail)
	}
	if !time.Now().Before(j.Message.ExpiresAt) {
		return finish("expired", nil)
	}
	session, err := s.Store.Session(ctx, j.Message.Recipient)
	if err != nil {
		return finish("pending", err)
	}
	if j.Epoch != 0 && j.Epoch != session.Epoch {
		return finish("stale", fmt.Errorf("recipient epoch changed"))
	}
	if j.Message.RecipientSession != "" && j.Message.RecipientSession != session.ID {
		return finish("stale", fmt.Errorf("recipient session changed"))
	}
	if j.Epoch == 0 {
		if err = s.Store.Bind(ctx, j, session.Epoch); err != nil {
			return err
		}
	}
	if session.Runtime == "none" {
		return finish("unsupported", delivery.ErrUnsupported)
	}
	if s.Adapter == nil {
		s.Adapter = delivery.Codex{}
	}
	// Refresh immediately before crossing the native delivery boundary.
	if !time.Now().Before(j.Message.ExpiresAt) {
		return finish("expired", nil)
	}
	err = s.Adapter.Deliver(ctx, session, j.Message)
	if err == nil {
		return finish("accepted", nil)
	}
	if errors.Is(err, delivery.ErrUnsupported) {
		return finish("unsupported", err)
	}
	if errors.Is(err, delivery.ErrAmbiguous) {
		return finish("ambiguous", err)
	}
	if j.Attempts >= 7 {
		return finish("failed", err)
	}
	return finish("pending", err)
}
func (s *Server) processBroker(ctx context.Context) {
	jobs, err := s.Store.Jobs(ctx, "broker", time.Now())
	if err != nil {
		return
	}
	for _, j := range jobs {
		if !time.Now().Before(j.Message.ExpiresAt) {
			_ = s.Store.Finish(ctx, j, "expired", "")
			continue
		}
		pctx, cancel := context.WithTimeout(ctx, time.Second)
		err = s.broker.Publish(pctx, j.Message)
		cancel()
		state, detail := "published", ""
		if err != nil {
			state = "pending"
			detail = err.Error()
		}
		if e := s.Store.Finish(ctx, j, state, detail); e != nil {
			log.Printf("broker persistence: %v", e)
		}
	}
	rows, err := s.Store.DB.QueryContext(ctx, "SELECT agent FROM sessions")
	if err != nil {
		return
	}
	var agents []string
	for rows.Next() {
		var a string
		if rows.Scan(&a) == nil {
			agents = append(agents, a)
		}
	}
	rows.Close()
	for _, a := range agents {
		if s.subscriptions[a] == nil {
			sub, e := s.broker.Subscribe(a)
			if e != nil {
				continue
			}
			s.subscriptions[a] = sub
		}
		err = s.broker.Poll(ctx, s.subscriptions[a], a, s.Store.Receive)
		if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, nats.ErrTimeout) && ctx.Err() == nil {
			log.Printf("broker receive for %s: %v", a, err)
		}
	}
}

// Call performs one cancellation-safe socket request. stream receives event responses until EOF.
func Call(ctx context.Context, dir string, r Request, stream func(json.RawMessage) error) (json.RawMessage, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(dir, "daemon.sock"))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-done:
		}
	}()
	if err = json.NewEncoder(conn).Encode(r); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(conn)
	for {
		var resp struct {
			Data  json.RawMessage `json:"data"`
			Error string          `json:"error"`
		}
		if err = dec.Decode(&resp); err != nil {
			if stream != nil && errors.Is(err, io.EOF) {
				return nil, nil
			}
			return nil, err
		}
		if resp.Error != "" {
			return nil, errors.New(resp.Error)
		}
		if stream == nil {
			return resp.Data, nil
		}
		if err = stream(resp.Data); err != nil {
			return nil, err
		}
	}
}
