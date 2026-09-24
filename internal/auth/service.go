package auth

import (
	"errors"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// Error bisnis yang dikenali handler untuk memetakan status ke HTTP
var (
	ErrEmailTaken         = errors.New("Email sudah terdaftar")
	ErrInvalidCredentials = errors.New("Email atau password salah")
	ErrPasswordTooShort   = errors.New("Password minimal 8 characters")
)

type Service struct {
	r         *Repository
	jwtSecret string
}

func NewService(r *Repository, jwtSecret string) *Service {
	return &Service{r: r, jwtSecret: jwtSecret}
}

// Register
func (s *Service) Register(email, fullName, password string) (*User, error) {
	// Validasi panjang minimal.
	if len(password) < 8 {
		return nil, ErrPasswordTooShort
	}

	// Check email
	exist, err := s.r.FindByEmail(email)
	if err != nil {
		return nil, err
	}
	if exist != nil {
		return nil, ErrEmailTaken
	}

	// Hash password
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	// Role default user
	user := &User{
		Email:        email,
		FullName:     fullName,
		PasswordHash: string(hash),
		Role:         "user",
	}
	if err := s.r.Create(user); err != nil {
		return nil, err
	}

	return user, nil
}

// Login
func (s *Service) Login(email, password string) (string, error) {
	user, err := s.r.FindByEmail(email)
	if err != nil {
		return "", err
	}

	// Email tidak ada -> use error yang sama dengan password salah
	if user == nil {
		return "", ErrInvalidCredentials
	}

	// Compare Hash&Pass
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return "", ErrInvalidCredentials
	}

	// cocok => buat token
	return s.GenerateToken(user)
}

func (s *Service) GenerateToken(user *User) (string, error) {
	// claims = isitoken. Mapclaims = map sederhana
	claims := jwt.MapClaims{
		"sub":  strconv.FormatInt(user.ID, 10),
		"role": user.Role,
		"exp":  time.Now().Add(24 * time.Hour).Unix(),
		"iat":  time.Now().Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	return token.SignedString([]byte(s.jwtSecret))
}
