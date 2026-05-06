package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

type User struct {
	ID        int64     `json:"id"`
	Login     string    `json:"login"`
	CreatedAt time.Time `json:"created_at"`
}

type WeightPreset struct {
	ID        int64           `json:"id"`
	Name      string          `json:"name"`
	Profile   string          `json:"profile"`
	Weights   json.RawMessage `json:"weights"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

func (s *Store) EnsureAppSchema(ctx context.Context) error {
	_, err := s.db.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS app_users (
			id BIGSERIAL PRIMARY KEY,
			login TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);

		DO $$
		BEGIN
			IF EXISTS (
				SELECT 1
				FROM information_schema.columns
				WHERE table_name = 'app_users'
					AND column_name = 'email'
			) AND NOT EXISTS (
				SELECT 1
				FROM information_schema.columns
				WHERE table_name = 'app_users'
					AND column_name = 'login'
			) THEN
				ALTER TABLE app_users RENAME COLUMN email TO login;
			END IF;
		END $$;

		CREATE TABLE IF NOT EXISTS app_sessions (
			id BIGSERIAL PRIMARY KEY,
			user_id BIGINT NOT NULL REFERENCES app_users(id) ON DELETE CASCADE,
			token_hash TEXT NOT NULL UNIQUE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			expires_at TIMESTAMPTZ NOT NULL
		);

		CREATE INDEX IF NOT EXISTS idx_app_sessions_user_id
			ON app_sessions (user_id);

		CREATE INDEX IF NOT EXISTS idx_app_sessions_expires_at
			ON app_sessions (expires_at);

		CREATE TABLE IF NOT EXISTS weight_presets (
			id BIGSERIAL PRIMARY KEY,
			user_id BIGINT NOT NULL REFERENCES app_users(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			profile TEXT NOT NULL,
			weights_json JSONB NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			UNIQUE (user_id, name)
		);

		CREATE INDEX IF NOT EXISTS idx_weight_presets_user_updated
			ON weight_presets (user_id, updated_at DESC);
	`)
	return err
}

func (s *Store) CreateUser(ctx context.Context, login string, password string) (User, string, error) {
	login = normalizeLogin(login)
	if login == "" {
		return User{}, "", fmt.Errorf("login is required")
	}
	if len(password) < 6 {
		return User{}, "", fmt.Errorf("password must be at least 6 characters")
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, "", err
	}

	var user User
	err = s.db.QueryRow(ctx, `
		INSERT INTO app_users (login, password_hash)
		VALUES ($1, $2)
		RETURNING id, login, created_at
	`, login, string(passwordHash)).Scan(&user.ID, &user.Login, &user.CreatedAt)
	if err != nil {
		return User{}, "", err
	}

	token, err := s.CreateSession(ctx, user.ID)
	if err != nil {
		return User{}, "", err
	}
	return user, token, nil
}

func (s *Store) AuthenticateUser(ctx context.Context, login string, password string) (User, string, error) {
	login = normalizeLogin(login)

	var user User
	var passwordHash string
	err := s.db.QueryRow(ctx, `
		SELECT id, login, password_hash, created_at
		FROM app_users
		WHERE login = $1
	`, login).Scan(&user.ID, &user.Login, &passwordHash, &user.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, "", ErrNotFound
		}
		return User{}, "", err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)); err != nil {
		return User{}, "", ErrNotFound
	}

	token, err := s.CreateSession(ctx, user.ID)
	if err != nil {
		return User{}, "", err
	}
	return user, token, nil
}

func (s *Store) CreateSession(ctx context.Context, userID int64) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	_, err = s.db.Exec(ctx, `
		INSERT INTO app_sessions (user_id, token_hash, expires_at)
		VALUES ($1, $2, $3)
	`, userID, tokenHash(token), time.Now().UTC().Add(30*24*time.Hour))
	if err != nil {
		return "", err
	}
	return token, nil
}

func (s *Store) UserBySessionToken(ctx context.Context, token string) (User, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return User{}, ErrNotFound
	}

	var user User
	err := s.db.QueryRow(ctx, `
		SELECT u.id, u.login, u.created_at
		FROM app_sessions s
		JOIN app_users u ON u.id = s.user_id
		WHERE s.token_hash = $1
			AND s.expires_at > now()
	`, tokenHash(token)).Scan(&user.ID, &user.Login, &user.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, err
	}
	return user, nil
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.db.Exec(ctx, `
		DELETE FROM app_sessions
		WHERE token_hash = $1
	`, tokenHash(token))
	return err
}

func (s *Store) CreateWeightPreset(ctx context.Context, userID int64, name string, profile string, weights json.RawMessage) (WeightPreset, error) {
	name = strings.TrimSpace(name)
	profile = strings.TrimSpace(profile)
	if name == "" {
		return WeightPreset{}, fmt.Errorf("preset name is required")
	}
	if profile == "" {
		return WeightPreset{}, fmt.Errorf("preset profile is required")
	}
	if len(weights) == 0 {
		weights = json.RawMessage(`{}`)
	}

	var preset WeightPreset
	err := s.db.QueryRow(ctx, `
		INSERT INTO weight_presets (
			user_id,
			name,
			profile,
			weights_json
		)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id, name)
		DO UPDATE SET
			profile = EXCLUDED.profile,
			weights_json = EXCLUDED.weights_json,
			updated_at = now()
		RETURNING id, name, profile, weights_json, created_at, updated_at
	`,
		userID,
		name,
		profile,
		weights,
	).Scan(&preset.ID, &preset.Name, &preset.Profile, &preset.Weights, &preset.CreatedAt, &preset.UpdatedAt)
	return preset, err
}

func (s *Store) ListWeightPresets(ctx context.Context, userID int64) ([]WeightPreset, error) {
	rows, err := s.db.Query(ctx, `
		SELECT
			id,
			name,
			profile,
			weights_json,
			created_at,
			updated_at
		FROM weight_presets
		WHERE user_id = $1
		ORDER BY updated_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]WeightPreset, 0)
	for rows.Next() {
		var item WeightPreset
		if err := rows.Scan(
			&item.ID,
			&item.Name,
			&item.Profile,
			&item.Weights,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *Store) DeleteWeightPreset(ctx context.Context, userID int64, presetID int64) error {
	tag, err := s.db.Exec(ctx, `
		DELETE FROM weight_presets
		WHERE user_id = $1
			AND id = $2
	`, userID, presetID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func normalizeLogin(login string) string {
	return strings.ToLower(strings.TrimSpace(login))
}

func randomToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
