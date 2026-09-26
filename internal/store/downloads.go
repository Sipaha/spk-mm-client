package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Download is one entry of the downloads list. Times are unix ms.
type Download struct {
	ID         int64
	ServerID   int64
	FileID     string
	Name       string
	Path       string // final path, once done
	Size       int64
	Mime       string
	StartedAt  int64
	FinishedAt int64
	State      string // DownloadRunning | DownloadDone | DownloadFailed
	Error      string // API error code of a failed download
}

// Download states.
const (
	DownloadRunning = "downloading"
	DownloadDone    = "done"
	DownloadFailed  = "failed"
	// DownloadInterrupted is the error of a download the app quit during.
	DownloadInterrupted = "interrupted"
)

// MaxDownloads bounds the list; older entries are dropped on insert.
const MaxDownloads = 100

const downloadCols = `id, server_id, file_id, name, path, size, mime, started_at, finished_at, state, error`

// newestFirst orders the list: a raised entry gets a new started_at.
const newestFirst = ` ORDER BY started_at DESC, id DESC`

// onTop is a started_at of at least ? that puts the row above every other
// (two downloads within one millisecond still keep their order).
const onTop = `MAX(?, COALESCE((SELECT MAX(started_at) FROM downloads), 0) + 1)`

func scanDownload(sc interface{ Scan(...any) error }) (Download, error) {
	var d Download
	err := sc.Scan(&d.ID, &d.ServerID, &d.FileID, &d.Name, &d.Path, &d.Size, &d.Mime, &d.StartedAt, &d.FinishedAt, &d.State, &d.Error)
	return d, err
}

// AddDownload records a download in progress and trims the list to
// MaxDownloads.
func (s *Store) AddDownload(ctx context.Context, d Download) (int64, error) {
	var id int64
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO downloads(server_id, file_id, name, size, mime, started_at, state) VALUES (?, ?, ?, ?, ?, `+onTop+`, ?)`,
			d.ServerID, d.FileID, d.Name, d.Size, d.Mime, d.StartedAt, DownloadRunning)
		if err != nil {
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM downloads WHERE id NOT IN (SELECT id FROM downloads`+newestFirst+` LIMIT ?)`, MaxDownloads)
		return err
	})
	return id, err
}

// FinishDownload ends a download: done at path when errCode is empty,
// failed with errCode otherwise.
func (s *Store) FinishDownload(ctx context.Context, id int64, path, errCode string, at int64) error {
	state := DownloadDone
	if errCode != "" {
		state, path = DownloadFailed, ""
	}
	_, err := s.db.ExecContext(ctx, `UPDATE downloads SET state = ?, path = ?, error = ?, finished_at = ? WHERE id = ?`,
		state, path, errCode, at, id)
	return err
}

// RaiseDownload moves an entry to the top of the list; false: there is no
// such entry (the user removed it).
func (s *Store) RaiseDownload(ctx context.Context, id int64, at int64) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE downloads SET started_at = `+onTop+` WHERE id = ?`, at, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (s *Store) GetDownload(ctx context.Context, id int64) (Download, error) {
	d, err := scanDownload(s.db.QueryRowContext(ctx, `SELECT `+downloadCols+` FROM downloads WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Download{}, ErrNotFound
	}
	return d, err
}

// ListDownloads returns the list, newest first.
func (s *Store) ListDownloads(ctx context.Context) ([]Download, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+downloadCols+` FROM downloads`+newestFirst)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Download
	for rows.Next() {
		d, err := scanDownload(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// RemoveDownload drops a finished or failed entry from the list (the file
// stays on disk); a download in progress is kept.
func (s *Store) RemoveDownload(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM downloads WHERE id = ? AND state != ?`, id, DownloadRunning)
	return err
}

// ClearDownloads drops every finished and failed entry.
func (s *Store) ClearDownloads(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM downloads WHERE state != ?`, DownloadRunning)
	return err
}

// failInterrupted marks downloads left running by the previous run as
// failed: nothing resumes them. One UPDATE on open.
func (s *Store) failInterrupted(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE downloads SET state = ?, error = ?, finished_at = ? WHERE state = ?`,
		DownloadFailed, DownloadInterrupted, time.Now().UnixMilli(), DownloadRunning)
	return err
}
