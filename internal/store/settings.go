// 系统级键值设置的 SQLite 持久化（如 tofu provider 下载代理）。
package store

import "fmt"

// GetSetting 读取配置值；不存在返回空串（不视为错误）。
func (s *SQLiteStore) GetSetting(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errorsIs(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("store: get setting %q: %w", key, err)
	}
	return v, nil
}

// SetSetting 写入（upsert）配置值。
func (s *SQLiteStore) SetSetting(key, value string) error {
	if key == "" {
		return fmt.Errorf("store: SetSetting: empty key")
	}
	if _, err := s.db.Exec(
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value); err != nil {
		return fmt.Errorf("store: set setting %q: %w", key, err)
	}
	return nil
}
