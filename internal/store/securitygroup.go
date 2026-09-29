// 命名安全组（端口集合）的 SQLite 持久化。
package store

import (
	"fmt"
	"strings"
	"time"

	"github.com/linuxsuren/open-cloud-web/internal/model"
)

const sgCols = `id, user_id, name, ports, udp_ports, remark, created_at, updated_at`

func scanSG(row interface{ Scan(...any) error }) (*model.SecurityGroup, error) {
	var g model.SecurityGroup
	var ports, udpPorts, createdAt, updatedAt string
	err := row.Scan(&g.ID, &g.UserID, &g.Name, &ports, &udpPorts, &g.Remark, &createdAt, &updatedAt)
	if errorsIs(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: scan security group: %w", err)
	}
	for _, p := range strings.Split(ports, ",") {
		if p == "" {
			continue
		}
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(p), "%d", &n); err == nil {
			g.Ports = append(g.Ports, n)
		}
	}
	for _, p := range strings.Split(udpPorts, ",") {
		if p == "" {
			continue
		}
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(p), "%d", &n); err == nil {
			g.UDPPorts = append(g.UDPPorts, n)
		}
	}
	if g.CreatedAt, err = parseTime(createdAt); err != nil {
		return nil, err
	}
	if g.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return nil, err
	}
	return &g, nil
}

func (s *SQLiteStore) CreateSecurityGroup(g *model.SecurityGroup) error {
	if g == nil || g.Name == "" || (len(g.Ports) == 0 && len(g.UDPPorts) == 0) {
		return fmt.Errorf("store: CreateSecurityGroup: name and ports required")
	}
	now := time.Now().UTC()
	if g.CreatedAt.IsZero() {
		g.CreatedAt = now
	}
	g.UpdatedAt = now
	var ps []string
	for _, p := range g.Ports {
		ps = append(ps, fmt.Sprint(p))
	}
	var us []string
	for _, p := range g.UDPPorts {
		us = append(us, fmt.Sprint(p))
	}
	res, err := s.db.Exec(
		`INSERT INTO security_groups (user_id, name, ports, udp_ports, remark, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		g.UserID, g.Name, strings.Join(ps, ","), strings.Join(us, ","), g.Remark,
		fmtTime(g.CreatedAt), fmtTime(g.UpdatedAt))
	if err != nil {
		return fmt.Errorf("store: create security group %q: %w", g.Name, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	g.ID = id
	return nil
}

func (s *SQLiteStore) GetSecurityGroup(id int64) (*model.SecurityGroup, error) {
	row := s.db.QueryRow(`SELECT `+sgCols+` FROM security_groups WHERE id = ?`, id)
	return scanSG(row)
}

// ListSecurityGroups 返回全局预置（user_id=0）与该用户自建的分组。
func (s *SQLiteStore) ListSecurityGroups(userID int64) ([]*model.SecurityGroup, error) {
	rows, err := s.db.Query(`SELECT `+sgCols+` FROM security_groups
		WHERE user_id = 0 OR user_id = ? ORDER BY user_id, id`, userID)
	if err != nil {
		return nil, fmt.Errorf("store: list security groups: %w", err)
	}
	defer rows.Close()
	var out []*model.SecurityGroup
	for rows.Next() {
		g, err := scanSG(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) DeleteSecurityGroup(id int64) error {
	res, err := s.db.Exec(`DELETE FROM security_groups WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store: delete security group %d: %w", id, err)
	}
	return ensureAffected(res, "delete security group", id)
}
