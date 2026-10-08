// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/pressly/goose/v3"

	"github.com/ripmav/streamcrew/internal/domain/command"
)

// goMigrations are the migrations that need Go, numbered together with the
// SQL files in migrations/ (Code-ADR-0008). sqlc reads only the SQL files,
// so a Go migration changes data and indexes, not columns.
func goMigrations() []*goose.Migration {
	return []*goose.Migration{
		goose.NewGoMigration(14, &goose.GoFunc{RunTx: commandNameKeysUp}, &goose.GoFunc{RunTx: commandNameKeysDown}),
	}
}

// commandNameKeysUp fills commands.name_key with command.NameKey, which
// knows the case of every script, and makes it unique (commands.md, B7).
// Of commands whose names differ in case only, the oldest keeps its name;
// the others get the first free suffix " (2)", " (3)" and so on.
func commandNameKeysUp(ctx context.Context, tx *sql.Tx) error {
	type row struct{ id, name string }
	rows, err := tx.QueryContext(ctx, "SELECT id, name FROM commands ORDER BY created_at, id")
	if err != nil {
		return err
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.name); err != nil {
			return errors.Join(err, rows.Close())
		}
		all = append(all, r)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}

	used := make(map[string]bool, len(all))
	var duplicates []row
	for _, r := range all {
		if key := command.NameKey(r.name); !used[key] {
			used[key] = true
			if err := setNameKey(ctx, tx, r.id, r.name); err != nil {
				return err
			}
			continue
		}
		duplicates = append(duplicates, r)
	}
	for _, r := range duplicates {
		name := r.name
		for n := 2; used[command.NameKey(name)]; n++ {
			name = fmt.Sprintf("%s (%d)", r.name, n)
		}
		used[command.NameKey(name)] = true
		if err := setNameKey(ctx, tx, r.id, name); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, "CREATE UNIQUE INDEX commands_name_key ON commands (name_key)")
	return err
}

// setNameKey sets the name of the command commandID and its key.
func setNameKey(ctx context.Context, tx *sql.Tx, commandID, name string) error {
	_, err := tx.ExecContext(ctx, "UPDATE commands SET name = ?, name_key = ? WHERE id = ?", name, command.NameKey(name), commandID)
	return err
}

// commandNameKeysDown drops the index; renamed commands keep their names.
func commandNameKeysDown(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, "DROP INDEX commands_name_key")
	return err
}
