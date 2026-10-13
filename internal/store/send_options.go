package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aldoborrero/herdr-mailer/internal/identity"
	"github.com/aldoborrero/herdr-mailer/internal/record"
	"github.com/google/uuid"
)

type SendOptions struct {
	OperationID      string `json:"operationId,omitempty"`
	RecipientSession string `json:"recipientSession,omitempty"`
	RecipientEpoch   int64  `json:"recipientEpoch,omitempty"`
}

// SendWithOptions atomically persists both the retry key and the message/jobs/event.
// A replay returns the original timestamps and route, even after recipient rotation.
func (s *Store) SendWithOptions(ctx context.Context, token, recipient, body, kind, correlation string, ttl time.Duration, bridge bool, opts SendOptions, askAuthor ...string) (record.Message, error) {
	var m record.Message
	if ttl <= 0 || ttl > 30*24*time.Hour {
		return m, fmt.Errorf("ttl must be between 0 and 720h")
	}
	if opts.OperationID != "" && !validOperation(opts.OperationID) {
		return m, fmt.Errorf("invalid operation ID")
	}
	if (opts.RecipientSession != "" || opts.RecipientEpoch != 0) && (!record.ValidUUID(opts.RecipientSession) || opts.RecipientEpoch < 1) {
		return m, fmt.Errorf("recipient session and epoch must be a valid pair")
	}
	author := ""
	if len(askAuthor) > 0 {
		author = askAuthor[0]
	}
	raw, err := json.Marshal(struct {
		Recipient, Body, Kind, Correlation, AskAuthor string
		TTL                                           int64
		Bridge                                        bool
		Options                                       SendOptions
	}{recipient, body, kind, correlation, author, int64(ttl), bridge, opts})
	if err != nil {
		return m, err
	}
	fingerprint := identity.Hash(string(raw))
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return m, err
	}
	defer tx.Rollback()
	var session record.Session
	var sessionRaw string
	if err = tx.QueryRowContext(ctx, "SELECT record FROM sessions WHERE token=?", identity.Hash(token)).Scan(&sessionRaw); err != nil {
		return m, ErrUnauthorized
	}
	if err = json.Unmarshal([]byte(sessionRaw), &session); err != nil {
		return m, err
	}
	origin := s.Owner + "/" + session.Agent
	if opts.OperationID != "" {
		var oldHash, oldRaw string
		err = tx.QueryRowContext(ctx, "SELECT o.request_hash,m.record FROM send_operations o JOIN messages m ON m.id=o.message WHERE o.origin=? AND o.operation=?", origin, opts.OperationID).Scan(&oldHash, &oldRaw)
		if err == nil {
			if oldHash != fingerprint {
				return m, fmt.Errorf("operation ID reused with different content")
			}
			if err = json.Unmarshal([]byte(oldRaw), &m); err != nil {
				return m, err
			}
			return m, tx.Commit()
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return m, err
		}
	}
	if kind == "reply" {
		query := "SELECT record FROM messages WHERE message_id=? AND recipient=?"
		args := []any{correlation, session.Agent}
		if author != "" {
			query += " AND origin=?"
			args = append(args, s.Owner+"/"+author)
		}
		rows, e := tx.QueryContext(ctx, query, args...)
		if e != nil {
			return m, e
		}
		var found []record.Message
		for rows.Next() {
			var raw string
			var ask record.Message
			if e = rows.Scan(&raw); e == nil {
				e = json.Unmarshal([]byte(raw), &ask)
			}
			if e != nil {
				rows.Close()
				return m, e
			}
			found = append(found, ask)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return m, e
		}
		if len(found) != 1 || found[0].Kind != "ask" {
			return m, fmt.Errorf("reply requires one authorized ask; select author if ambiguous")
		}
		recipient = found[0].Author
	}
	now := time.Now().UTC()
	m = record.Message{Type: record.MessageType, ID: uuid.NewString(), Owner: s.Owner, Author: session.Agent, Recipient: recipient, Session: session.ID, Epoch: session.Epoch, CreatedAt: now, ExpiresAt: now.Add(ttl), Kind: kind, Correlation: correlation, Body: body}
	err = tx.QueryRowContext(ctx, "SELECT id,epoch FROM sessions WHERE agent=?", recipient).Scan(&m.RecipientSession, &m.RecipientEpoch)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return m, err
	}
	if opts.RecipientSession != "" && (m.RecipientSession != opts.RecipientSession || m.RecipientEpoch != opts.RecipientEpoch) {
		return m, fmt.Errorf("recipient session precondition failed")
	}
	added, err := s.persistTx(ctx, tx, m, bridge, token)
	if err != nil {
		return m, err
	}
	if opts.OperationID != "" {
		if _, err = tx.ExecContext(ctx, "INSERT INTO send_operations VALUES(?,?,?,?)", origin, opts.OperationID, fingerprint, m.DeliveryID()); err != nil {
			return m, err
		}
	}
	if err = tx.Commit(); err != nil {
		return m, err
	}
	if added {
		s.Notify()
	}
	return m, nil
}

func validOperation(s string) bool {
	if len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return s != ""
}
