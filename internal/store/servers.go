package store

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"time"
)

var (
	ErrNotFound     = errors.New("not found")
	ErrServerExists = errors.New("server already exists")
)

// Server is one configured Mattermost server. Token is the session token —
// it must never leave the Go side; LogValue masks it for slog.
type Server struct {
	ID        int64
	URL       string // normalized URL the user entered
	SiteURL   string // normalized SiteURL reported by the server ('' if unset)
	Name      string
	Sort      int
	Token     string
	UserID    string
	Username  string
	GitLab    bool
	CreatedAt int64
}

func (s Server) SignedIn() bool { return s.Token != "" }

func (s Server) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Int64("id", s.ID),
		slog.String("url", s.URL),
		slog.String("name", s.Name),
		slog.Bool("signed_in", s.SignedIn()),
		slog.String("username", s.Username),
	)
}

const serverCols = `id, url, site_url, name, sort, token, user_id, username, gitlab, created_at`

func scanServer(row interface{ Scan(...any) error }) (Server, error) {
	var s Server
	var gitlab int
	err := row.Scan(&s.ID, &s.URL, &s.SiteURL, &s.Name, &s.Sort, &s.Token, &s.UserID, &s.Username, &gitlab, &s.CreatedAt)
	s.GitLab = gitlab != 0
	return s, err
}

func (s *Store) AddServer(ctx context.Context, srv Server) (Server, error) {
	srv.CreatedAt = time.Now().UnixMilli()
	gitlab := 0
	if srv.GitLab {
		gitlab = 1
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO servers(url, site_url, name, sort, gitlab, created_at)
		 VALUES (?, ?, ?, (SELECT COALESCE(MAX(sort)+1, 0) FROM servers), ?, ?)`,
		srv.URL, srv.SiteURL, srv.Name, gitlab, srv.CreatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return Server{}, ErrServerExists
		}
		return Server{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Server{}, err
	}
	return s.GetServer(ctx, id)
}

func (s *Store) ListServers(ctx context.Context) ([]Server, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+serverCols+` FROM servers ORDER BY sort, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Server
	for rows.Next() {
		srv, err := scanServer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, srv)
	}
	return out, rows.Err()
}

func (s *Store) GetServer(ctx context.Context, id int64) (Server, error) {
	srv, err := scanServer(s.db.QueryRowContext(ctx, `SELECT `+serverCols+` FROM servers WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Server{}, ErrNotFound
	}
	return srv, err
}

func (s *Store) SetSession(ctx context.Context, id int64, token, userID, username string) error {
	return s.execOne(ctx, `UPDATE servers SET token = ?, user_id = ?, username = ? WHERE id = ?`, token, userID, username, id)
}

func (s *Store) ClearSession(ctx context.Context, id int64) error {
	return s.execOne(ctx, `UPDATE servers SET token = '', user_id = '', username = '' WHERE id = ?`, id)
}

func (s *Store) DeleteServer(ctx context.Context, id int64) error {
	return s.execOne(ctx, `DELETE FROM servers WHERE id = ?`, id)
}

func (s *Store) execOne(ctx context.Context, q string, args ...any) error {
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
