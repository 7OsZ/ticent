package auth

import (
	"errors"

	"gorm.io/gorm"
)

// Repository memegang handle DB. Disuntik lewat constructor, bukan global.
type Repository struct {
	db *gorm.DB
}

// NewRepository = constructor. Dipanggil sekali saat wiring di main.go.
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// Create menyimpan user baru. Pointer *User dipakai supaya GORM bisa mengisi balik ID dan CreatedAt hasil insert ke struct yang sama.
func (r *Repository) Create(user *User) error {
	return r.db.Create(user).Error
}

// FindByEmail mencari satu user berdasarkan email (dipakai saat login dan saat cek email sudah terpakai).

func (r *Repository) FindByEmail(email string) (*User, error) {
	var user User

	// Where + First = SELECT ... WHERE email = ? LIMIT 1.
	err := r.db.Where("email = ?", email).First(&user).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	return &user, nil
}
