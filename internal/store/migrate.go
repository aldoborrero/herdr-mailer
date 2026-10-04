package store

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
