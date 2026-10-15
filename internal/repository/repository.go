package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"checkin/internal/guest"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("guest not found")
var ErrAlrOnTheList = errors.New("guest is already on the list")

type Repository struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repository {
	return &Repository{
		pool: pool,
	}
}

func (r *Repository) Import(ctx context.Context, guests []guest.Guest) error {
	if len(guests) == 0 {
		return errors.New("guest list can't be empty")
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("tx begin: %w", err)
	}
	defer tx.Rollback(ctx)

	upsertSQL := `
		INSERT INTO guests (surname, first_name, patronymic, priority, full_name_key, spc_tag)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (full_name_key) DO UPDATE SET
			surname = EXCLUDED.surname, first_name = EXCLUDED.first_name,
			patronymic = EXCLUDED.patronymic, priority = EXCLUDED.priority, spc_tag = EXCLUDED.spc_tag,
			removed = false, source='file'
	`
	keys := make([]string, 0, len(guests))
	batch := &pgx.Batch{}
	for _, g := range guests {
		key := g.FullNameKey()
		keys = append(keys, key)
		batch.Queue(upsertSQL, g.Surname, g.FirstName, g.Patronymic, g.Priority, key, g.SpcTag)
	}

	br := tx.SendBatch(ctx, batch)
	for i := range guests {
		if _, err := br.Exec(); err != nil {
			br.Close()
			return fmt.Errorf("upsert %q: %w", keys[i], err)
		}
	}
	if err := br.Close(); err != nil {
		return fmt.Errorf("close batch: %w", err)
	}

	softDeleteSQL := `
		UPDATE guests SET removed = true
		WHERE NOT removed AND full_name_key <> ALL($1) AND source='file'
	`
	_, err = tx.Exec(ctx, softDeleteSQL, keys)
	if err != nil {
		return fmt.Errorf("soft delete: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	return nil
}

func (r *Repository) SetCheckIn(ctx context.Context, id int64, checkedIn, plusOne bool) (CheckInState, error) {
	setCheckInSQL := `
		UPDATE guests g SET checked_in = $2, plus_one = $3
		FROM (SELECT id, checked_in, plus_one FROM guests
			WHERE id = $1 AND NOT removed
			FOR UPDATE) old
		WHERE g.id = old.id
		RETURNING old.checked_in, old.plus_one	
	`
	var prev CheckInState
	err := r.pool.QueryRow(ctx, setCheckInSQL, id, checkedIn, plusOne).
		Scan(&prev.CheckedIn, &prev.PlusOne)
	if errors.Is(err, pgx.ErrNoRows) {
		return CheckInState{}, ErrNotFound
	}
	if err != nil {
		return CheckInState{}, fmt.Errorf("set checkin: %w", err)
	}
	return prev, nil
}

type CheckInState struct {
	CheckedIn bool
	PlusOne   bool
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func (r *Repository) Search(ctx context.Context, name string) ([]guest.Guest, error) {
	q := `
		SELECT id, source, surname, first_name, patronymic, priority, checked_in, plus_one, spc_tag, removed
		FROM guests
		WHERE NOT removed AND full_name_key LIKE ALL($1)
		ORDER BY surname, first_name, patronymic
		LIMIT 20
	`
	words := strings.Fields(guest.NormalizeName(name))
	if len(words) == 0 {
		return []guest.Guest{}, nil
	}
	patterns := make([]string, 0, len(words))
	for _, w := range words {
		patterns = append(patterns, "%"+likeEscaper.Replace(w)+"%")
	}
	rows, err := r.pool.Query(ctx, q, patterns)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	defer rows.Close()

	var guests []guest.Guest
	for rows.Next() {
		var g guest.Guest
		if err := rows.Scan(&g.ID, &g.Source, &g.Surname, &g.FirstName, &g.Patronymic,
			&g.Priority, &g.CheckedIn, &g.PlusOne, &g.SpcTag, &g.Removed,
		); err != nil {
			return nil, fmt.Errorf("scan guest: %w", err)
		}
		guests = append(guests, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search rows: %w", err)
	}
	if len(guests) == 0 {
		return []guest.Guest{}, nil
	}
	return guests, nil
}

func (r *Repository) List(ctx context.Context) ([]guest.Guest, error) {
	q := `
		SELECT id, source, surname, first_name, patronymic, priority, checked_in, plus_one, spc_tag, removed
		FROM guests
		ORDER BY surname, first_name, patronymic
	`
	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}
	defer rows.Close()

	var guests []guest.Guest
	for rows.Next() {
		var g guest.Guest
		if err := rows.Scan(&g.ID, &g.Source, &g.Surname, &g.FirstName, &g.Patronymic, &g.Priority, &g.CheckedIn, &g.PlusOne, &g.SpcTag, &g.Removed); err != nil {
			return nil, fmt.Errorf("scan guest: %w", err)
		}
		guests = append(guests, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list rows: %w", err)
	}
	if len(guests) == 0 {
		return []guest.Guest{}, nil
	}
	return guests, nil
}

func (r *Repository) AddWalkIn(ctx context.Context, g guest.Guest) (int64, error) {
	q := `
		INSERT INTO guests (surname, first_name, patronymic, priority, full_name_key, source, checked_in, spc_tag)
		VALUES ($1, $2, $3, $4, $5, 'walkin', true, $6)
		ON CONFLICT (full_name_key) DO UPDATE SET
			surname = EXCLUDED.surname, first_name = EXCLUDED.first_name, patronymic = EXCLUDED.patronymic,
			priority = EXCLUDED.priority, spc_tag = EXCLUDED.spc_tag, source = 'walkin', removed = false, checked_in = true, plus_one = false
		WHERE guests.removed
		RETURNING id
	`

	var id int64
	err := r.pool.QueryRow(ctx, q, g.Surname, g.FirstName, g.Patronymic, g.Priority, g.FullNameKey(), g.SpcTag).
		Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrAlrOnTheList
	}
	if err != nil {
		return 0, fmt.Errorf("add walkin: %w", err)
	}
	return id, nil
}

func (r *Repository) AddStudents(ctx context.Context, n int) (int, error) {
	q := `
		UPDATE student_count
		SET total = total + $1
		RETURNING total
	`
	var count int
	err := r.pool.QueryRow(ctx, q, n).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("add students: %w", err)
	}
	return count, nil
}

func (r *Repository) SetStudents(ctx context.Context, n int) (int, error) {
	q := `
		UPDATE student_count
		SET total = $1
		RETURNING total
	`
	var count int
	err := r.pool.QueryRow(ctx, q, n).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("set students: %w", err)
	}
	return count, nil
}

func (r *Repository) Counters(ctx context.Context) (Counters, error) {
	q := `
		SELECT
  			count(*) FILTER (WHERE NOT removed AND priority = 'regular' AND source = 'file'),
  			count(*) FILTER (WHERE NOT removed AND priority = 'vip' AND source = 'file'),
  			count(*) FILTER (WHERE checked_in AND priority = 'regular'),
  			count(*) FILTER (WHERE checked_in AND priority = 'vip'),
  			count(*) FILTER (WHERE plus_one),
			(SELECT total FROM student_count)
		FROM guests
	`
	var c Counters
	err := r.pool.QueryRow(ctx, q).Scan(&c.InvitedRegular, &c.InvitedVIP,
		&c.ArrivedRegular, &c.ArrivedVIP, &c.PlusOnes, &c.StudentCount)
	if err != nil {
		return Counters{}, fmt.Errorf("counters: %w", err)
	}
	return c, nil
}

type Counters struct {
	InvitedRegular int64
	InvitedVIP     int64
	ArrivedRegular int64
	ArrivedVIP     int64
	PlusOnes       int64
	StudentCount   int64
}
