package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/aldoborrero/herdr-mailer/internal/identity"
	"github.com/aldoborrero/herdr-mailer/internal/record"
	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

var ErrUnauthorized = errors.New("unauthorized or stale session")
var ErrAskResolved = errors.New("ask already resolved by another reply")

type Store struct {
	DB      *sql.DB
	Owner   string
	mu      sync.Mutex
	changed chan struct{}
}
type Event struct {
	Cursor    int64  `json:"cursor"`
	Kind      string `json:"kind"`
	MessageID string `json:"messageId"`
	Origin    string `json:"origin"`
}
type Job struct {
	Key       string
	Message   record.Message
	Channel   string
	Epoch     int64
	Attempts  int
	State     string
	LastError string
}

func Open(dir, owner string) (*Store, error) {
	if err := identity.PrivateDir(dir); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "inbox.sqlite")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	f.Close()
	if err := identity.PrivateFile(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{DB: db, Owner: owner, changed: make(chan struct{})}
	_, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL; PRAGMA busy_timeout=5000; PRAGMA foreign_keys=ON;
 CREATE TABLE IF NOT EXISTS metadata(key TEXT PRIMARY KEY, value TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS sessions(agent TEXT PRIMARY KEY, id TEXT NOT NULL, epoch INTEGER NOT NULL, token TEXT NOT NULL UNIQUE, record TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS messages(id TEXT PRIMARY KEY, recipient TEXT NOT NULL, record TEXT NOT NULL, unread INTEGER NOT NULL DEFAULT 1);
 CREATE TABLE IF NOT EXISTS jobs(id TEXT NOT NULL REFERENCES messages(id), channel TEXT NOT NULL, epoch INTEGER NOT NULL, attempts INTEGER NOT NULL DEFAULT 0, next INTEGER NOT NULL DEFAULT 0, state TEXT NOT NULL DEFAULT 'pending', error TEXT NOT NULL DEFAULT '', PRIMARY KEY(id,channel));
 CREATE TABLE IF NOT EXISTS events(cursor INTEGER PRIMARY KEY AUTOINCREMENT, recipient TEXT NOT NULL, kind TEXT NOT NULL, message TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS recipient_events ON events(recipient,cursor);
 CREATE INDEX IF NOT EXISTS pending_jobs ON jobs(state,next);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	if _, err = db.Exec("INSERT INTO metadata(key,value) VALUES('owner',?) ON CONFLICT(key) DO NOTHING", owner); err != nil {
		db.Close()
		return nil, err
	}
	var storedOwner string
	if err = db.QueryRow("SELECT value FROM metadata WHERE key='owner'").Scan(&storedOwner); err != nil || storedOwner != owner {
		db.Close()
		return nil, fmt.Errorf("database owner mismatch or unreadable metadata")
	}
	if err = s.migrateOrigins(); err != nil {
		db.Close()
		return nil, err
	}
	if err = s.migrateResolutions(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close() error { return s.DB.Close() }
func (s *Store) Notify() {
	s.mu.Lock()
	close(s.changed)
	s.changed = make(chan struct{})
	s.mu.Unlock()
}
func (s *Store) Changed() <-chan struct{} { s.mu.Lock(); defer s.mu.Unlock(); return s.changed }
func (s *Store) Register(ctx context.Context, agent, runtime, location string) (record.Session, string, error) {
	var r record.Session
	if !record.ValidAgent(agent) {
		return r, "", fmt.Errorf("invalid agent")
	}
	if runtime != "codex" && runtime != "pi" && runtime != "claude" && runtime != "none" {
		return r, "", fmt.Errorf("invalid runtime")
	}
	if runtime != "none" && !record.ValidUUID(location) {
		return r, "", fmt.Errorf("runtime location must be exact session UUID")
	}
	token := identity.Token()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return r, "", err
	}
	defer tx.Rollback()
	var epoch int64
	err = tx.QueryRowContext(ctx, "SELECT epoch FROM sessions WHERE agent=?", agent).Scan(&epoch)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return r, "", err
	}
	r = record.Session{Type: record.SessionType, Owner: s.Owner, Agent: agent, ID: uuid.NewString(), Epoch: epoch + 1, Runtime: runtime, Location: location, CreatedAt: time.Now().UTC()}
	b, _ := json.Marshal(r)
	_, err = tx.ExecContext(ctx, `INSERT INTO sessions VALUES(?,?,?,?,?) ON CONFLICT(agent) DO UPDATE SET id=excluded.id,epoch=excluded.epoch,token=excluded.token,record=excluded.record`, agent, r.ID, r.Epoch, identity.Hash(token), string(b))
	if err == nil {
		err = tx.Commit()
	}
	if err == nil {
		s.Notify()
	}
	return r, token, err
}
func (s *Store) Authenticate(ctx context.Context, token string) (record.Session, error) {
	var r record.Session
	var b string
	err := s.DB.QueryRowContext(ctx, "SELECT record FROM sessions WHERE token=?", identity.Hash(token)).Scan(&b)
	if err != nil {
		return r, ErrUnauthorized
	}
	err = json.Unmarshal([]byte(b), &r)
	return r, err
}
func (s *Store) Session(ctx context.Context, agent string) (record.Session, error) {
	var r record.Session
	var b string
	err := s.DB.QueryRowContext(ctx, "SELECT record FROM sessions WHERE agent=?", agent).Scan(&b)
	if err == nil {
		err = json.Unmarshal([]byte(b), &r)
	}
	return r, err
}
func (s *Store) Send(ctx context.Context, token, recipient, body, kind, correlation string, ttl time.Duration, bridge bool, askAuthor ...string) (record.Message, error) {
	var m record.Message
	if ttl <= 0 || ttl > 30*24*time.Hour {
		return m, fmt.Errorf("ttl must be between 0 and 720h")
	}
	session, err := s.Authenticate(ctx, token)
	if err != nil {
		return m, err
	}
	if kind == "reply" {
		ask, e := s.Read(ctx, session.Agent, correlation, false, askAuthor...)
		if e != nil {
			return m, e
		}
		if ask.Kind != "ask" {
			return m, fmt.Errorf("correlation is not an ask")
		}
		recipient = ask.Author
	}
	now := time.Now().UTC()
	m = record.Message{Type: record.MessageType, ID: uuid.NewString(), Owner: s.Owner, Author: session.Agent, Recipient: recipient, Session: session.ID, Epoch: session.Epoch, CreatedAt: now, ExpiresAt: now.Add(ttl), Kind: kind, Correlation: correlation, Body: body}
	// Carry explicit routing through broker delays, rather than rebinding on receipt.
	target, err := s.Session(ctx, recipient)
	if err == nil {
		m.RecipientSession, m.RecipientEpoch = target.ID, target.Epoch
	} else if !errors.Is(err, sql.ErrNoRows) {
		return m, err
	}
	_, err = s.persist(ctx, m, bridge, token)
	return m, err
}

// Receive must complete before a broker delivery can be acknowledged.
func (s *Store) Receive(ctx context.Context, m record.Message) (bool, error) {
	return s.persist(ctx, m, false, "")
}
func (s *Store) persist(ctx context.Context, m record.Message, bridge bool, token string) (bool, error) {
	if err := m.Validate(); err != nil {
		return false, err
	}
	if m.Owner != s.Owner {
		return false, fmt.Errorf("untrusted owner")
	}
	b, err := json.Marshal(m)
	if err != nil {
		return false, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if token != "" {
		var current string
		err = tx.QueryRowContext(ctx, "SELECT id FROM sessions WHERE token=? AND agent=? AND epoch=?", identity.Hash(token), m.Author, m.Epoch).Scan(&current)
		if err != nil || current != m.Session {
			return false, ErrUnauthorized
		}
	}
	var askKey string
	if m.Kind == "reply" {
		var raw string
		if err = tx.QueryRowContext(ctx, "SELECT id,record FROM messages WHERE origin=? AND message_id=?", m.Owner+"/"+m.Recipient, m.Correlation).Scan(&askKey, &raw); err != nil {
			return false, fmt.Errorf("reply ask unavailable")
		}
		var ask record.Message
		if err = json.Unmarshal([]byte(raw), &ask); err != nil {
			return false, err
		}
		if ask.Kind != "ask" || ask.Owner != m.Owner || ask.Author != m.Recipient || ask.Recipient != m.Author {
			return false, fmt.Errorf("unauthorized reply correlation")
		}
		var resolved string
		err = tx.QueryRowContext(ctx, "SELECT m.record FROM ask_resolutions r JOIN messages m ON m.id=r.reply WHERE r.ask=?", askKey).Scan(&resolved)
		if err == nil {
			if resolved != string(b) {
				return false, ErrAskResolved
			}
			// Only the exact accepted reply can be replayed. No message, job or event is added.
			return false, tx.Commit()
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
	}
	key := m.DeliveryID()
	res, err := tx.ExecContext(ctx, "INSERT INTO messages(id,origin,message_id,recipient,record) VALUES(?,?,?,?,?) ON CONFLICT(origin,message_id) DO NOTHING", key, m.Origin(), m.ID, m.Recipient, string(b))
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		var old string
		if err = tx.QueryRowContext(ctx, "SELECT id,record FROM messages WHERE origin=? AND message_id=?", m.Origin(), m.ID).Scan(&key, &old); err != nil {
			return false, err
		}
		if old != string(b) {
			return false, fmt.Errorf("message ID collision")
		}
	}
	if askKey != "" {
		if _, err = tx.ExecContext(ctx, "INSERT INTO ask_resolutions(ask,reply) VALUES(?,?)", askKey, key); err != nil {
			return false, err
		}
	}
	if n == 0 {
		return false, tx.Commit()
	}
	var epoch int64
	err = tx.QueryRowContext(ctx, "SELECT epoch FROM sessions WHERE agent=?", m.Recipient).Scan(&epoch)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if m.RecipientEpoch != 0 {
		epoch = m.RecipientEpoch
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO jobs(id,channel,epoch) VALUES(?,'native',?)", key, epoch)
	if err != nil {
		return false, err
	}
	if bridge {
		if _, err = tx.ExecContext(ctx, "INSERT INTO jobs(id,channel,epoch) VALUES(?,'broker',0)", key); err != nil {
			return false, err
		}
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO events(recipient,kind,message) VALUES(?,'received',?)", m.Recipient, key)
	if err != nil {
		return false, err
	}
	err = tx.Commit()
	if err == nil {
		s.Notify()
	}
	return true, err
}
func (s *Store) Inbox(ctx context.Context, agent string, unread bool) ([]record.Message, error) {
	q := "SELECT record FROM messages WHERE recipient=?"
	if unread {
		q += " AND unread=1"
	}
	q += " ORDER BY rowid"
	rows, err := s.DB.QueryContext(ctx, q, agent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []record.Message{}
	for rows.Next() {
		var b string
		var m record.Message
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(b), &m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
func (s *Store) Read(ctx context.Context, agent, id string, mark bool, author ...string) (record.Message, error) {
	var m record.Message
	q := "SELECT id,record FROM messages WHERE message_id=? AND recipient=?"
	args := []any{id, agent}
	if len(author) > 0 && author[0] != "" {
		q += " AND origin=?"
		args = append(args, s.Owner+"/"+author[0])
	}
	rows, err := s.DB.QueryContext(ctx, q+" LIMIT 2", args...)
	if err != nil {
		return m, err
	}
	var key, b string
	if !rows.Next() {
		rows.Close()
		return m, fmt.Errorf("message unavailable")
	}
	if err = rows.Scan(&key, &b); err != nil {
		rows.Close()
		return m, err
	}
	if rows.Next() {
		rows.Close()
		return m, fmt.Errorf("ambiguous message ID; specify --author")
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return m, err
	}
	if err = json.Unmarshal([]byte(b), &m); err != nil {
		return m, err
	}
	if mark {
		_, err = s.DB.ExecContext(ctx, "UPDATE messages SET unread=0 WHERE id=? AND recipient=?", key, agent)
	}
	return m, err
}
func (s *Store) Events(ctx context.Context, agent string, cursor int64) ([]Event, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT e.cursor,e.kind,m.message_id,m.origin FROM events e JOIN messages m ON m.id=e.message WHERE e.recipient=? AND e.cursor>? ORDER BY e.cursor LIMIT 100", agent, cursor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		if err = rows.Scan(&e.Cursor, &e.Kind, &e.MessageID, &e.Origin); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
func (s *Store) Jobs(ctx context.Context, channel string, now time.Time) ([]Job, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT m.id,m.record,j.epoch,j.attempts FROM jobs j JOIN messages m ON m.id=j.id WHERE j.channel=? AND j.state='pending' AND j.next<=? ORDER BY m.rowid LIMIT 100`, channel, now.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		j := Job{Channel: channel}
		var b string
		if err = rows.Scan(&j.Key, &b, &j.Epoch, &j.Attempts); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(b), &j.Message); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
func (s *Store) Bind(ctx context.Context, j Job, epoch int64) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE jobs SET epoch=? WHERE id=? AND channel=? AND epoch=0", epoch, j.Key, j.Channel)
	return err
}
func (s *Store) Finish(ctx context.Context, j Job, state, detail string) error {
	delay := time.Second * time.Duration(1<<min(j.Attempts, 8))
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, "UPDATE jobs SET attempts=attempts+1,next=?,state=?,error=? WHERE id=? AND channel=?", time.Now().Add(delay).UnixMilli(), state, detail, j.Key, j.Channel)
	if err != nil {
		return err
	}
	if state != "pending" {
		_, err = tx.ExecContext(ctx, "INSERT INTO events(recipient,kind,message) VALUES(?,?,?)", j.Message.Recipient, j.Channel+":"+state, j.Key)
		if err != nil {
			return err
		}
	}
	err = tx.Commit()
	if err == nil {
		s.Notify()
	}
	return err
}
func (s *Store) Status(ctx context.Context, agent string) (map[string]int, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT j.channel || ':' || j.state,count(*) FROM jobs j JOIN messages m ON m.id=j.id WHERE m.recipient=? GROUP BY 1", agent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		if err = rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		out[k] = n
	}
	return out, rows.Err()
}
