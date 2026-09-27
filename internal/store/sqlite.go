package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/linuxsuren/open-cloud-web/internal/model"

	_ "modernc.org/sqlite"
)

// SQLiteStore 基于 modernc.org/sqlite（纯 Go，无 CGO）的 Store 实现。
// 内部通过 database/sql 连接池使用，可安全并发调用。
// 所有时间统一以 UTC 存储。
type SQLiteStore struct {
	db *sql.DB
}

// 编译期保证接口实现完整。
var _ Store = (*SQLiteStore)(nil)

const schema = `
CREATE TABLE IF NOT EXISTS users (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    username        TEXT    NOT NULL,
    display_name    TEXT    NOT NULL DEFAULT '',
    email           TEXT    NOT NULL DEFAULT '',
    role            TEXT    NOT NULL,
    status          TEXT    NOT NULL,
    provider        TEXT    NOT NULL,
    provider_sub    TEXT    NOT NULL DEFAULT '',
    password_hash    TEXT    NOT NULL DEFAULT '',
    max_duration_sec INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT    NOT NULL,
    updated_at      TEXT    NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_username ON users(username);
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_provider_sub ON users(provider, provider_sub);

CREATE TABLE IF NOT EXISTS pats (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id      INTEGER NOT NULL,
    name         TEXT    NOT NULL,
    token_hash   TEXT    NOT NULL,
    expires_at   TEXT    NOT NULL,
    created_at   TEXT    NOT NULL,
    last_used_at TEXT,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_pats_token_hash ON pats(token_hash);
CREATE INDEX IF NOT EXISTS idx_pats_user_id ON pats(user_id);

CREATE TABLE IF NOT EXISTS instances (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id        INTEGER NOT NULL,
    name           TEXT    NOT NULL DEFAULT '',
    provider       TEXT    NOT NULL,
    region         TEXT    NOT NULL DEFAULT '',
    zone           TEXT    NOT NULL DEFAULT '',
    image_id       TEXT    NOT NULL DEFAULT '',
    instance_type  TEXT    NOT NULL DEFAULT '',
    status         TEXT    NOT NULL,
    expires_at     TEXT    NOT NULL,
    renewed_at     TEXT,
    duration_sec   INTEGER NOT NULL DEFAULT 0,
    public_ip      TEXT    NOT NULL DEFAULT '',
    private_ip     TEXT    NOT NULL DEFAULT '',
    tf_workspace   TEXT    NOT NULL DEFAULT '',
    error_message  TEXT    NOT NULL DEFAULT '',
    created_at     TEXT    NOT NULL,
    updated_at     TEXT    NOT NULL,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_instances_status_expires ON instances(status, expires_at);
CREATE INDEX IF NOT EXISTS idx_instances_user_id ON instances(user_id);

CREATE TABLE IF NOT EXISTS audit_logs (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER NOT NULL,
    action     TEXT    NOT NULL,
    detail     TEXT    NOT NULL DEFAULT '',
    created_at TEXT    NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_audit_logs_created_at ON audit_logs(created_at);
`

// OpenSQLite 打开（必要时创建）SQLite 数据库并建表。
// dsn 示例：file:/path/to/ocw.db?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)
func OpenSQLite(dsn string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open sqlite: %w", err)
	}
	// SQLite 单写多读：限制写并发，配合 busy_timeout 避免锁冲突。
	db.SetMaxOpenConns(1)
	s := &SQLiteStore{db: db}
	if err := s.init(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// OpenSQLitePath 按文件路径打开 SQLite（自动附加推荐 PRAGMA）。
func OpenSQLitePath(path string) (*SQLiteStore, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	return OpenSQLite(dsn)
}

func (s *SQLiteStore) init() error {
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("store: init schema: %w", err)
	}
	return nil
}

// Close 关闭底层连接池。
func (s *SQLiteStore) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("store: close: %w", err)
	}
	return nil
}

func fmtTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("store: parse time %q: %w", s, err)
	}
	return t.UTC(), nil
}

// ---------- user ----------

func (s *SQLiteStore) CreateUser(u *model.User) error {
	if u == nil {
		return fmt.Errorf("store: CreateUser: nil user")
	}
	if u.Username == "" {
		return fmt.Errorf("store: CreateUser: empty username")
	}
	if !u.Role.Valid() {
		return fmt.Errorf("store: CreateUser: invalid role %q", u.Role)
	}
	if !u.Status.Valid() {
		return fmt.Errorf("store: CreateUser: invalid status %q", u.Status)
	}
	now := time.Now().UTC()
	if u.CreatedAt.IsZero() {
		u.CreatedAt = now
	}
	u.UpdatedAt = now
	u.CreatedAt, u.UpdatedAt = u.CreatedAt.UTC(), u.UpdatedAt.UTC()

	res, err := s.db.Exec(
		`INSERT INTO users (username, display_name, email, role, status, provider, provider_sub, max_duration_sec, password_hash, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.Username, u.DisplayName, u.Email, string(u.Role), string(u.Status),
		u.Provider, u.ProviderSub, u.MaxDurationSec, u.PasswordHash, fmtTime(u.CreatedAt), fmtTime(u.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("store: create user %q: %w", u.Username, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: create user %q: last insert id: %w", u.Username, err)
	}
	u.ID = id
	return nil
}

const userCols = `id, username, display_name, email, role, status, provider, provider_sub, max_duration_sec, password_hash, created_at, updated_at`

func scanUser(row interface{ Scan(...any) error }) (*model.User, error) {
	var u model.User
	var role, status, createdAt, updatedAt string
	err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Email, &role, &status,
		&u.Provider, &u.ProviderSub, &u.MaxDurationSec, &u.PasswordHash, &createdAt, &updatedAt)
	if errorsIs(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: scan user: %w", err)
	}
	u.Role, u.Status = model.Role(role), model.UserStatus(status)
	if u.CreatedAt, err = parseTime(createdAt); err != nil {
		return nil, err
	}
	if u.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *SQLiteStore) GetUser(id int64) (*model.User, error) {
	row := s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE id = ?`, id)
	return scanUser(row)
}

func (s *SQLiteStore) GetUserByUsername(username string) (*model.User, error) {
	if username == "" {
		return nil, ErrNotFound
	}
	row := s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE username = ?`, username)
	return scanUser(row)
}

func (s *SQLiteStore) GetUserByProvider(provider, sub string) (*model.User, error) {
	if provider == "" || sub == "" {
		return nil, ErrNotFound
	}
	row := s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE provider = ? AND provider_sub = ?`, provider, sub)
	return scanUser(row)
}

func (s *SQLiteStore) ListUsers() ([]*model.User, error) {
	rows, err := s.db.Query(`SELECT ` + userCols + ` FROM users ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("store: list users: %w", err)
	}
	defer rows.Close()
	var out []*model.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) UpdateUser(u *model.User) error {
	if u == nil || u.ID <= 0 {
		return fmt.Errorf("store: UpdateUser: invalid user id")
	}
	if !u.Role.Valid() || !u.Status.Valid() {
		return fmt.Errorf("store: UpdateUser: invalid role/status")
	}
	u.UpdatedAt = time.Now().UTC()
	res, err := s.db.Exec(
		`UPDATE users SET username=?, display_name=?, email=?, role=?, status=?, provider=?, provider_sub=?, max_duration_sec=?, updated_at=? WHERE id=?`,
		u.Username, u.DisplayName, u.Email, string(u.Role), string(u.Status),
		u.Provider, u.ProviderSub, u.MaxDurationSec, fmtTime(u.UpdatedAt), u.ID,
	)
	if err != nil {
		return fmt.Errorf("store: update user %d: %w", u.ID, err)
	}
	return ensureAffected(res, "update user", u.ID)
}

func (s *SQLiteStore) DeleteUser(id int64) error {
	res, err := s.db.Exec(`DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store: delete user %d: %w", id, err)
	}
	return ensureAffected(res, "delete user", id)
}

// SetUserPasswordHash 保存本地用户的 bcrypt 哈希（ PasswordHashSetter 能力）。
// UpdateUser 有意不覆盖 password_hash，避免常规更新抹掉凭据。
func (s *SQLiteStore) SetUserPasswordHash(userID int64, hash string) error {
	if userID <= 0 {
		return fmt.Errorf("store: SetUserPasswordHash: invalid user id %d", userID)
	}
	res, err := s.db.Exec(`UPDATE users SET password_hash=? WHERE id=?`, hash, userID)
	if err != nil {
		return fmt.Errorf("store: set password hash for user %d: %w", userID, err)
	}
	return ensureAffected(res, "set password hash", userID)
}

// ---------- PAT ----------

func (s *SQLiteStore) CreatePAT(userID int64, name, tokenHash string, expiresAt time.Time) (int64, error) {
	if userID <= 0 {
		return 0, fmt.Errorf("store: CreatePAT: invalid user id %d", userID)
	}
	if name == "" {
		return 0, fmt.Errorf("store: CreatePAT: empty name")
	}
	if len(tokenHash) != 64 { // SHA-256 hex
		return 0, fmt.Errorf("store: CreatePAT: token hash must be sha-256 hex")
	}
	now := time.Now().UTC()
	res, err := s.db.Exec(
		`INSERT INTO pats (user_id, name, token_hash, expires_at, created_at) VALUES (?, ?, ?, ?, ?)`,
		userID, name, tokenHash, fmtTime(expiresAt), fmtTime(now),
	)
	if err != nil {
		return 0, fmt.Errorf("store: create pat: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: create pat: last insert id: %w", err)
	}
	return id, nil
}

const patCols = `id, user_id, name, token_hash, expires_at, created_at, last_used_at`

func scanPAT(row interface{ Scan(...any) error }) (*model.PAT, error) {
	var p model.PAT
	var expiresAt, createdAt string
	var lastUsed sql.NullString
	err := row.Scan(&p.ID, &p.UserID, &p.Name, &p.TokenHash, &expiresAt, &createdAt, &lastUsed)
	if errorsIs(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: scan pat: %w", err)
	}
	if p.ExpiresAt, err = parseTime(expiresAt); err != nil {
		return nil, err
	}
	if p.CreatedAt, err = parseTime(createdAt); err != nil {
		return nil, err
	}
	if lastUsed.Valid && lastUsed.String != "" {
		t, err := parseTime(lastUsed.String)
		if err != nil {
			return nil, err
		}
		p.LastUsedAt = &t
	}
	return &p, nil
}

func (s *SQLiteStore) GetPATByHash(tokenHash string) (*model.PAT, error) {
	if tokenHash == "" {
		return nil, ErrNotFound
	}
	row := s.db.QueryRow(`SELECT `+patCols+` FROM pats WHERE token_hash = ?`, tokenHash)
	return scanPAT(row)
}

func (s *SQLiteStore) ListPATs(userID int64) ([]*model.PAT, error) {
	rows, err := s.db.Query(`SELECT `+patCols+` FROM pats WHERE user_id = ? ORDER BY id`, userID)
	if err != nil {
		return nil, fmt.Errorf("store: list pats for user %d: %w", userID, err)
	}
	defer rows.Close()
	var out []*model.PAT
	for rows.Next() {
		p, err := scanPAT(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) DeletePAT(id int64) error {
	res, err := s.db.Exec(`DELETE FROM pats WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store: delete pat %d: %w", id, err)
	}
	return ensureAffected(res, "delete pat", id)
}

// TouchPAT 更新 PAT 的最后使用时间（认证成功时调用）。
func (s *SQLiteStore) TouchPAT(id int64, usedAt time.Time) error {
	res, err := s.db.Exec(`UPDATE pats SET last_used_at = ? WHERE id = ?`, fmtTime(usedAt), id)
	if err != nil {
		return fmt.Errorf("store: touch pat %d: %w", id, err)
	}
	return ensureAffected(res, "touch pat", id)
}

// ---------- instance ----------

const instanceCols = `id, user_id, name, provider, region, zone, image_id, instance_type, status,
expires_at, renewed_at, duration_sec, public_ip, private_ip, tf_workspace, error_message, created_at, updated_at`

func scanInstance(row interface{ Scan(...any) error }) (*model.Instance, error) {
	var in model.Instance
	var status, expiresAt, createdAt, updatedAt string
	var renewedAt sql.NullString
	err := row.Scan(&in.ID, &in.UserID, &in.Name, &in.Provider, &in.Region, &in.Zone,
		&in.ImageID, &in.InstanceType, &status, &expiresAt, &renewedAt, &in.DurationSec,
		&in.PublicIP, &in.PrivateIP, &in.TfWorkspace, &in.ErrorMessage, &createdAt, &updatedAt)
	if errorsIs(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: scan instance: %w", err)
	}
	in.Status = model.InstanceStatus(status)
	if in.ExpiresAt, err = parseTime(expiresAt); err != nil {
		return nil, err
	}
	if renewedAt.Valid && renewedAt.String != "" {
		t, err := parseTime(renewedAt.String)
		if err != nil {
			return nil, err
		}
		in.RenewedAt = &t
	}
	if in.CreatedAt, err = parseTime(createdAt); err != nil {
		return nil, err
	}
	if in.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return nil, err
	}
	return &in, nil
}

func (s *SQLiteStore) CreateInstance(in *model.Instance) error {
	if in == nil {
		return fmt.Errorf("store: CreateInstance: nil instance")
	}
	if in.UserID <= 0 {
		return fmt.Errorf("store: CreateInstance: invalid user id %d", in.UserID)
	}
	if in.Provider == "" {
		return fmt.Errorf("store: CreateInstance: empty provider")
	}
	if !in.Status.Valid() {
		return fmt.Errorf("store: CreateInstance: invalid status %q", in.Status)
	}
	now := time.Now().UTC()
	if in.CreatedAt.IsZero() {
		in.CreatedAt = now
	}
	in.UpdatedAt = now
	in.CreatedAt, in.UpdatedAt = in.CreatedAt.UTC(), in.UpdatedAt.UTC()

	var renewedAt any
	if in.RenewedAt != nil {
		renewedAt = fmtTime(*in.RenewedAt)
	}
	res, err := s.db.Exec(
		`INSERT INTO instances (user_id, name, provider, region, zone, image_id, instance_type, status,
			expires_at, renewed_at, duration_sec, public_ip, private_ip, tf_workspace, error_message, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.UserID, in.Name, in.Provider, in.Region, in.Zone, in.ImageID, in.InstanceType,
		string(in.Status), fmtTime(in.ExpiresAt), renewedAt, in.DurationSec,
		in.PublicIP, in.PrivateIP, in.TfWorkspace, in.ErrorMessage, fmtTime(in.CreatedAt), fmtTime(in.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("store: create instance: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: create instance: last insert id: %w", err)
	}
	in.ID = id
	return nil
}

func (s *SQLiteStore) GetInstance(id int64) (*model.Instance, error) {
	row := s.db.QueryRow(`SELECT `+instanceCols+` FROM instances WHERE id = ?`, id)
	return scanInstance(row)
}

func (s *SQLiteStore) ListInstancesByUser(userID int64) ([]*model.Instance, error) {
	rows, err := s.db.Query(`SELECT `+instanceCols+` FROM instances WHERE user_id = ? ORDER BY id DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("store: list instances for user %d: %w", userID, err)
	}
	defer rows.Close()
	var out []*model.Instance
	for rows.Next() {
		in, err := scanInstance(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) ListInstancesByStatuses(statuses []model.InstanceStatus) ([]*model.Instance, error) {
	if len(statuses) == 0 {
		return nil, fmt.Errorf("store: ListInstancesByStatuses: empty statuses")
	}
	// 全部参数化，杜绝 SQL 注入；占位符仅由我们生成。
	placeholders := make([]string, len(statuses))
	args := make([]any, len(statuses))
	for i, st := range statuses {
		if !st.Valid() {
			return nil, fmt.Errorf("store: ListInstancesByStatuses: invalid status %q", st)
		}
		placeholders[i] = "?"
		args[i] = string(st)
	}
	q := `SELECT ` + instanceCols + ` FROM instances WHERE status IN (` +
		strings.Join(placeholders, ",") + `) ORDER BY expires_at`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list instances by statuses: %w", err)
	}
	defer rows.Close()
	var out []*model.Instance
	for rows.Next() {
		in, err := scanInstance(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) UpdateInstance(in *model.Instance) error {
	if in == nil || in.ID <= 0 {
		return fmt.Errorf("store: UpdateInstance: invalid instance id")
	}
	if !in.Status.Valid() {
		return fmt.Errorf("store: UpdateInstance: invalid status %q", in.Status)
	}
	in.UpdatedAt = time.Now().UTC()
	var renewedAt any
	if in.RenewedAt != nil {
		renewedAt = fmtTime(*in.RenewedAt)
	}
	res, err := s.db.Exec(
		`UPDATE instances SET user_id=?, name=?, provider=?, region=?, zone=?, image_id=?, instance_type=?,
		 status=?, expires_at=?, renewed_at=?, duration_sec=?, public_ip=?, private_ip=?, tf_workspace=?,
		 error_message=?, updated_at=? WHERE id=?`,
		in.UserID, in.Name, in.Provider, in.Region, in.Zone, in.ImageID, in.InstanceType,
		string(in.Status), fmtTime(in.ExpiresAt), renewedAt, in.DurationSec,
		in.PublicIP, in.PrivateIP, in.TfWorkspace, in.ErrorMessage, fmtTime(in.UpdatedAt), in.ID,
	)
	if err != nil {
		return fmt.Errorf("store: update instance %d: %w", in.ID, err)
	}
	return ensureAffected(res, "update instance", in.ID)
}

// ---------- audit ----------

func (s *SQLiteStore) CreateAuditLog(log *model.AuditLog) error {
	if log == nil {
		return fmt.Errorf("store: CreateAuditLog: nil log")
	}
	if log.Action == "" {
		return fmt.Errorf("store: CreateAuditLog: empty action")
	}
	if log.CreatedAt.IsZero() {
		log.CreatedAt = time.Now().UTC()
	}
	log.CreatedAt = log.CreatedAt.UTC()
	res, err := s.db.Exec(
		`INSERT INTO audit_logs (user_id, action, detail, created_at) VALUES (?, ?, ?, ?)`,
		log.UserID, log.Action, log.Detail, fmtTime(log.CreatedAt),
	)
	if err != nil {
		return fmt.Errorf("store: create audit log: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: create audit log: last insert id: %w", err)
	}
	log.ID = id
	return nil
}

func (s *SQLiteStore) ListAuditLogs(limit int) ([]*model.AuditLog, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	rows, err := s.db.Query(
		`SELECT id, user_id, action, detail, created_at FROM audit_logs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list audit logs: %w", err)
	}
	defer rows.Close()
	var out []*model.AuditLog
	for rows.Next() {
		var l model.AuditLog
		var createdAt string
		if err := rows.Scan(&l.ID, &l.UserID, &l.Action, &l.Detail, &createdAt); err != nil {
			return nil, fmt.Errorf("store: scan audit log: %w", err)
		}
		if l.CreatedAt, err = parseTime(createdAt); err != nil {
			return nil, err
		}
		out = append(out, &l)
	}
	return out, rows.Err()
}

// ---------- helpers ----------

// errorsIs 避免与内建 errors.Is 命名冲突：仅判断 sql.ErrNoRows。
func errorsIs(err error) bool { return err == sql.ErrNoRows }

func ensureAffected(res sql.Result, op string, id int64) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: %s %d: rows affected: %w", op, id, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
