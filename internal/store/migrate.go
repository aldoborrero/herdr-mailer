package store

import (
	"encoding/json"
	"fmt"
	"github.com/aldoborrero/herdr-mailer/internal/record"
)

// migrateOrigins upgrades initial MVP databases without replacing message rows,
// foreign keys, pending jobs or event cursors. The database is bound to one owner.
func (s *Store) migrateOrigins() error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var present int
	if err = tx.QueryRow("SELECT count(*) FROM pragma_table_info('messages') WHERE name='origin'").Scan(&present); err != nil {
		return err
	}
	if present == 0 {
		if _, err = tx.Exec(`ALTER TABLE messages ADD COLUMN origin TEXT NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN message_id TEXT NOT NULL DEFAULT '';
UPDATE messages SET origin=json_extract(record,'$.owner') || '/' || json_extract(record,'$.author'),message_id=json_extract(record,'$.id');`); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("CREATE UNIQUE INDEX IF NOT EXISTS origin_message_id ON messages(origin,message_id)"); err != nil {
		return err
	}
	return tx.Commit()
}

// migrateResolutions backfills valid previous replies once. Inconsistent history
// fails closed; upgrading must not silently choose a winner or delete evidence.
func (s *Store) migrateResolutions() error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`CREATE TABLE IF NOT EXISTS ask_resolutions(ask TEXT PRIMARY KEY REFERENCES messages(id),reply TEXT NOT NULL UNIQUE REFERENCES messages(id));`); err != nil {
		return err
	}
	var version int
	if err = tx.QueryRow("SELECT count(*) FROM metadata WHERE key='reply_resolution_schema' AND value='1'").Scan(&version); err != nil {
		return err
	}
	if version == 1 {
		return tx.Commit()
	}
	rows, err := tx.Query(`SELECT COALESCE(a.id,''),COALESCE(a.record,''),r.id,r.record FROM messages r LEFT JOIN messages a ON a.origin=json_extract(r.record,'$.owner') || '/' || json_extract(r.record,'$.recipient') AND a.message_id=json_extract(r.record,'$.correlation') WHERE json_extract(r.record,'$.kind')='reply' ORDER BY r.rowid`)
	if err != nil {
		return err
	}
	type resolution struct{ askKey, replyKey string }
	var resolutions []resolution
	for rows.Next() {
		var r resolution
		var askRaw, replyRaw string
		if err = rows.Scan(&r.askKey, &askRaw, &r.replyKey, &replyRaw); err != nil {
			rows.Close()
			return err
		}
		var ask, reply record.Message
		if json.Unmarshal([]byte(askRaw), &ask) != nil || json.Unmarshal([]byte(replyRaw), &reply) != nil || r.askKey == "" || ask.Kind != "ask" || reply.Owner != s.Owner || ask.Owner != reply.Owner || reply.Author != ask.Recipient || reply.Recipient != ask.Author {
			rows.Close()
			return fmt.Errorf("cannot upgrade: unauthorized or uncorrelated reply in existing state")
		}
		resolutions = append(resolutions, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, r := range resolutions {
		if _, err = tx.Exec("INSERT INTO ask_resolutions(ask,reply) VALUES(?,?)", r.askKey, r.replyKey); err != nil {
			return fmt.Errorf("cannot upgrade: conflicting reply resolutions: %w", err)
		}
	}
	if _, err = tx.Exec("INSERT INTO metadata(key,value) VALUES('reply_resolution_schema','1')"); err != nil {
		return err
	}
	return tx.Commit()
}
