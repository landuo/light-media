package app

import (
	"context"
	"database/sql"
	"encoding/json"

	"golang.org/x/crypto/bcrypt"
)

func (a *App) bootstrapAdmin(name, password string) error {
	hash, e := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if e != nil {
		return e
	}
	_, e = a.Store.DB.Exec(`INSERT INTO admin(id,username,password_hash) VALUES(1,?,?)`, name, hash)
	if e != nil {
		return e
	}
	return syncAdminUser(context.Background(), a.Store.DB, name, hash)
}

// syncAdminUser projects the single administrator into MoviePilot's user table.
// The admin table stays the credential authority until the auth path is
// switched, but every write also refreshes the MoviePilot row.
func syncAdminUser(ctx context.Context, db *sql.DB, name string, hash []byte) error {
	raw, err := json.Marshal(map[string]any{
		"username": name, "password_hash": hash, "is_active": 1, "is_superuser": 1,
		"permissions": map[string]any{}, "settings": map[string]any{},
	})
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, recordInsertSQL("user", recordTableForKind("user")), name, raw, now())
	return err
}
