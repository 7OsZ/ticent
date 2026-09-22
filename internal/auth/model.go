package auth

import "time"

type User struct {
	ID           int64     `gorm:"primaryKey" json:"id"`
	Email        string    `json:"email"`
	FullName     string    `json:"full_name"`
	PasswordHash string    `json:"-"`
	Role         string    `json:"role"`
	CreatedAt    time.Time `json:"created_at"`
}

// TableName memaksa GORM memakai nama tabel "users".
func (User) TableName() string {
	return "users"
}
