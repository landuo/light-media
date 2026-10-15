package app

import (
	"golang.org/x/crypto/bcrypt"
)

func (a *App) bootstrapAdmin(name, password string) error {
	hash, e := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if e != nil {
		return e
	}
	_, e = a.Store.DB.Exec(`INSERT INTO admin(id,username,password_hash) VALUES(1,?,?)`, name, hash)
	return e
}
