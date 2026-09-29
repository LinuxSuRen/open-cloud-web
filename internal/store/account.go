// 云账号（用户添加的云提供商认证信息）的 SQLite 持久化。
package store

import (
	"fmt"
	"time"

	"github.com/linuxsuren/open-cloud-web/internal/model"
)

const accountCols = `id, user_id, name, provider, access_key, secret_enc, session_enc, region, created_at, updated_at`

func scanAccount(row interface{ Scan(...any) error }) (*model.CloudAccount, error) {
	var a model.CloudAccount
	var createdAt, updatedAt string
	err := row.Scan(&a.ID, &a.UserID, &a.Name, &a.Provider, &a.AccessKey, &a.SecretEnc,
		&a.SessionEnc, &a.Region, &createdAt, &updatedAt)
	if errorsIs(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: scan cloud account: %w", err)
	}
	if a.CreatedAt, err = parseTime(createdAt); err != nil {
		return nil, err
	}
	if a.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return nil, err
	}
	return &a, nil
}

func validProvider(p string) bool {
	return p == "alicloud" || p == "volcengine"
}

func (s *SQLiteStore) CreateCloudAccount(a *model.CloudAccount) error {
	if a == nil || a.UserID <= 0 {
		return fmt.Errorf("store: CreateCloudAccount: invalid account")
	}
	if a.Name == "" || !validProvider(a.Provider) {
		return fmt.Errorf("store: CreateCloudAccount: name required, provider must be alicloud|volcengine")
	}
	now := time.Now().UTC()
	if a.CreatedAt.IsZero() {
		a.CreatedAt = now
	}
	a.UpdatedAt = now
	res, err := s.db.Exec(
		`INSERT INTO cloud_accounts (user_id, name, provider, access_key, secret_enc, session_enc, region, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.UserID, a.Name, a.Provider, a.AccessKey, a.SecretEnc, a.SessionEnc, a.Region,
		fmtTime(a.CreatedAt), fmtTime(a.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("store: create cloud account %q: %w", a.Name, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: create cloud account %q: last insert id: %w", a.Name, err)
	}
	a.ID = id
	return nil
}

func (s *SQLiteStore) GetCloudAccount(id int64) (*model.CloudAccount, error) {
	row := s.db.QueryRow(`SELECT `+accountCols+` FROM cloud_accounts WHERE id = ?`, id)
	return scanAccount(row)
}

func (s *SQLiteStore) ListCloudAccountsByUser(userID int64) ([]*model.CloudAccount, error) {
	rows, err := s.db.Query(`SELECT `+accountCols+` FROM cloud_accounts WHERE user_id = ? ORDER BY id`, userID)
	if err != nil {
		return nil, fmt.Errorf("store: list cloud accounts of user %d: %w", userID, err)
	}
	defer rows.Close()
	var out []*model.CloudAccount
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) ListCloudAccounts() ([]*model.CloudAccount, error) {
	rows, err := s.db.Query(`SELECT ` + accountCols + ` FROM cloud_accounts ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("store: list cloud accounts: %w", err)
	}
	defer rows.Close()
	var out []*model.CloudAccount
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) UpdateCloudAccount(a *model.CloudAccount) error {
	if a == nil || a.ID <= 0 {
		return fmt.Errorf("store: UpdateCloudAccount: invalid account id")
	}
	if a.Name == "" || !validProvider(a.Provider) {
		return fmt.Errorf("store: UpdateCloudAccount: name required, provider must be alicloud|volcengine")
	}
	a.UpdatedAt = time.Now().UTC()
	res, err := s.db.Exec(
		`UPDATE cloud_accounts SET name=?, provider=?, access_key=?, secret_enc=?, session_enc=?, region=?, updated_at=? WHERE id=?`,
		a.Name, a.Provider, a.AccessKey, a.SecretEnc, a.SessionEnc, a.Region, fmtTime(a.UpdatedAt), a.ID,
	)
	if err != nil {
		return fmt.Errorf("store: update cloud account %d: %w", a.ID, err)
	}
	return ensureAffected(res, "update cloud account", a.ID)
}

func (s *SQLiteStore) DeleteCloudAccount(id int64) error {
	res, err := s.db.Exec(`DELETE FROM cloud_accounts WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store: delete cloud account %d: %w", id, err)
	}
	return ensureAffected(res, "delete cloud account", id)
}

// GetInstanceByWorkspace 按 tofu workspace 名查实例（调度器销毁时
// 恢复云账号凭据用）。
func (s *SQLiteStore) GetInstanceByWorkspace(workspace string) (*model.Instance, error) {
	if workspace == "" {
		return nil, ErrNotFound
	}
	row := s.db.QueryRow(`SELECT `+instanceCols+` FROM instances WHERE tf_workspace = ?`, workspace)
	return scanInstance(row)
}
