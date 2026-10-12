package accounts

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

// MaxNoteLength is the longest journal note, in characters.
const MaxNoteLength = 1000

var ErrNoteTooLong = errors.New("note too long")

// SetFillNote saves the account's journal note on one of its fills; an empty note deletes
// it. ErrNotFound means the fill doesn't exist or belongs to another account.
func (s *Store) SetFillNote(ctx context.Context, account, fill uuid.UUID, note string) error {
	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) > MaxNoteLength {
		return ErrNoteTooLong
	}
	var owned bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM fills WHERE id = $1 AND account_id = $2)`,
		fill, account).Scan(&owned); err != nil {
		return fmt.Errorf("set fill note: %w", err)
	}
	if !owned {
		return ErrNotFound
	}
	var err error
	if note == "" {
		_, err = s.pool.Exec(ctx, `DELETE FROM fill_notes WHERE fill_id = $1`, fill)
	} else {
		_, err = s.pool.Exec(ctx, `
			INSERT INTO fill_notes (fill_id, account_id, note) VALUES ($1, $2, $3)
			ON CONFLICT (fill_id) DO UPDATE SET note = EXCLUDED.note, updated_at = now()`, fill, account, note)
	}
	if err != nil {
		return fmt.Errorf("set fill note: %w", err)
	}
	return nil
}
