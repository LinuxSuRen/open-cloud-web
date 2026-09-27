package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/linuxsuren/open-cloud-web/internal/model"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	dir := t.TempDir()
	s, err := OpenSQLitePath(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("OpenSQLitePath: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func mustUser(t *testing.T, s *SQLiteStore, username string) *model.User {
	t.Helper()
	u := &model.User{
		Username: username, DisplayName: username, Email: username + "@example.com",
		Role: model.RoleUser, Status: model.StatusActive,
		Provider: "local", ProviderSub: "local-" + username, // (provider, provider_sub) 唯一索引要求 local 用户也需唯一 sub
	}
	if err := s.CreateUser(u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return u
}

func TestUserCRUD(t *testing.T) {
	s := newTestStore(t)

	u := mustUser(t, s, "alice")
	if u.ID == 0 || u.CreatedAt.IsZero() || u.UpdatedAt.IsZero() {
		t.Fatalf("unexpected user after create: %+v", u)
	}
	if u.CreatedAt.Location() != time.UTC {
		t.Errorf("CreatedAt not UTC: %v", u.CreatedAt)
	}

	got, err := s.GetUser(u.ID)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if got.Username != "alice" || got.Role != model.RoleUser || got.Status != model.StatusActive {
		t.Errorf("round-trip mismatch: %+v", got)
	}

	if _, err := s.GetUser(9999); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetUser missing: err = %v, want ErrNotFound", err)
	}

	byName, err := s.GetUserByUsername("alice")
	if err != nil || byName.ID != u.ID {
		t.Errorf("GetUserByUsername: %v %+v", err, byName)
	}
	if _, err := s.GetUserByUsername("nobody"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetUserByUsername missing: err = %v", err)
	}

	// 唯一约束
	dup := &model.User{Username: "alice", Role: model.RoleUser, Status: model.StatusActive, Provider: "local"}
	if err := s.CreateUser(dup); err == nil {
		t.Error("duplicate username should fail")
	}

	// provider 查询
	f := &model.User{Username: "feishu-u", Role: model.RoleUser, Status: model.StatusActive,
		Provider: "feishu", ProviderSub: "sub-1"}
	if err := s.CreateUser(f); err != nil {
		t.Fatalf("CreateUser feishu: %v", err)
	}
	got, err = s.GetUserByProvider("feishu", "sub-1")
	if err != nil || got.ID != f.ID {
		t.Errorf("GetUserByProvider: %v %+v", err, got)
	}
	if _, err := s.GetUserByProvider("feishu", "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetUserByProvider missing: err = %v", err)
	}

	// 更新
	u.Role = model.RoleAdmin
	u.Status = model.StatusDisabled
	u.MaxDurationSec = 7200
	if err := s.UpdateUser(u); err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}
	got, _ = s.GetUser(u.ID)
	if got.Role != model.RoleAdmin || got.Status != model.StatusDisabled || got.MaxDurationSec != 7200 {
		t.Errorf("update not applied: %+v", got)
	}

	// 列表 + 删除
	users, err := s.ListUsers()
	if err != nil || len(users) != 2 {
		t.Errorf("ListUsers: %v len=%d", err, len(users))
	}
	if err := s.DeleteUser(u.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if err := s.DeleteUser(u.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("double delete: err = %v, want ErrNotFound", err)
	}

	// 输入校验
	if err := s.CreateUser(nil); err == nil {
		t.Error("nil user should fail")
	}
	if err := s.CreateUser(&model.User{Username: "", Role: model.RoleUser, Status: model.StatusActive, Provider: "local"}); err == nil {
		t.Error("empty username should fail")
	}
	if err := s.CreateUser(&model.User{Username: "x", Role: "bogus", Status: model.StatusActive, Provider: "local"}); err == nil {
		t.Error("invalid role should fail")
	}
}

func TestUserCascadeDelete(t *testing.T) {
	s := newTestStore(t)
	u := mustUser(t, s, "cascade-user")
	if _, err := s.CreatePAT(u.ID, "cli", sha256Hex("tok"), time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("CreatePAT: %v", err)
	}
	if err := s.DeleteUser(u.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	pats, err := s.ListPATs(u.ID)
	if err != nil || len(pats) != 0 {
		t.Errorf("PATs should cascade-delete: %v %d", err, len(pats))
	}
}

func TestPATCRUD(t *testing.T) {
	s := newTestStore(t)
	u := mustUser(t, s, "pat-owner")

	hash := sha256Hex("pat_ocw_secret")
	exp := time.Now().Add(24 * time.Hour).UTC()
	id, err := s.CreatePAT(u.ID, "cli-token", hash, exp)
	if err != nil {
		t.Fatalf("CreatePAT: %v", err)
	}
	if id == 0 {
		t.Fatal("pat id should be non-zero")
	}

	got, err := s.GetPATByHash(hash)
	if err != nil {
		t.Fatalf("GetPATByHash: %v", err)
	}
	if got.UserID != u.ID || got.Name != "cli-token" || got.TokenHash != hash {
		t.Errorf("PAT round-trip mismatch: %+v", got)
	}
	if !got.ExpiresAt.Equal(exp) {
		t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt, exp)
	}
	if got.LastUsedAt != nil {
		t.Errorf("LastUsedAt should be nil initially, got %v", got.LastUsedAt)
	}

	if _, err := s.GetPATByHash(sha256Hex("missing")); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetPATByHash missing: err = %v", err)
	}

	// TouchPAT（LastUsedAt 语义）
	used := time.Now().Add(time.Minute).UTC()
	if err := s.TouchPAT(id, used); err != nil {
		t.Fatalf("TouchPAT: %v", err)
	}
	got, _ = s.GetPATByHash(hash)
	if got.LastUsedAt == nil || !got.LastUsedAt.Equal(used) {
		t.Errorf("LastUsedAt = %v, want %v", got.LastUsedAt, used)
	}

	// 列表
	id2, err := s.CreatePAT(u.ID, "another", sha256Hex("tok2"), exp)
	if err != nil {
		t.Fatalf("CreatePAT 2: %v", err)
	}
	pats, err := s.ListPATs(u.ID)
	if err != nil || len(pats) != 2 {
		t.Errorf("ListPATs: %v len=%d", err, len(pats))
	}
	other := mustUser(t, s, "other")
	if pats, _ := s.ListPATs(other.ID); len(pats) != 0 {
		t.Errorf("ListPATs for other user should be empty, got %d", len(pats))
	}

	// 删除
	if err := s.DeletePAT(id2); err != nil {
		t.Fatalf("DeletePAT: %v", err)
	}
	if err := s.DeletePAT(id2); !errors.Is(err, ErrNotFound) {
		t.Errorf("double delete PAT: err = %v", err)
	}

	// 校验：非法 hash
	if _, err := s.CreatePAT(u.ID, "bad", "short", exp); err == nil {
		t.Error("short token hash should fail")
	}
}

func TestInstanceLifecycle(t *testing.T) {
	s := newTestStore(t)
	u := mustUser(t, s, "inst-owner")

	in := &model.Instance{
		UserID: u.ID, Name: "lab-1", Provider: "alicloud", Region: "cn-hangzhou", Zone: "cn-hangzhou-a",
		ImageID: "img-123", InstanceType: "ecs.t6-c1m1.large", Status: model.StatusCreating,
		ExpiresAt: time.Now().Add(time.Hour).UTC(), DurationSec: 3600,
		TfWorkspace: "ws-1",
	}
	if err := s.CreateInstance(in); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if in.ID == 0 {
		t.Fatal("instance id should be non-zero")
	}

	got, err := s.GetInstance(in.ID)
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	if got.Status != model.StatusCreating || got.RenewedAt != nil || got.Provider != "alicloud" {
		t.Errorf("round-trip mismatch: %+v", got)
	}

	if _, err := s.GetInstance(9999); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetInstance missing: err = %v", err)
	}

	// 转 Running
	got.Status = model.StatusRunning
	got.PublicIP = "1.2.3.4"
	if err := s.UpdateInstance(got); err != nil {
		t.Fatalf("UpdateInstance: %v", err)
	}
	got, _ = s.GetInstance(in.ID)
	if got.Status != model.StatusRunning || got.PublicIP != "1.2.3.4" {
		t.Errorf("update not applied: %+v", got)
	}
	if !model.CanTransition(model.StatusCreating, model.StatusRunning) {
		t.Error("creating->running should be legal")
	}

	// 续用语义：RenewedAt 置位 + ExpiresAt 延后
	now := time.Now().UTC()
	if !got.CanRenew(now) {
		t.Fatal("freshly running instance should be renewable")
	}
	renewed := now
	got.RenewedAt = &renewed
	got.ExpiresAt = now.Add(2 * time.Hour)
	got.DurationSec = 7200
	if err := s.UpdateInstance(got); err != nil {
		t.Fatalf("UpdateInstance renew: %v", err)
	}
	got, _ = s.GetInstance(in.ID)
	if got.RenewedAt == nil || !got.RenewedAt.Equal(renewed) {
		t.Errorf("renewed_at not persisted: %+v", got.RenewedAt)
	}
	if got.CanRenew(now) {
		t.Error("already-renewed instance must not be renewable")
	}
	if got.CanRenew(got.ExpiresAt.Add(time.Second)) {
		t.Error("expired instance must not be renewable")
	}

	// 按状态查询（调度器用）
	in2 := &model.Instance{UserID: u.ID, Provider: "volcengine", Status: model.StatusRunning,
		ExpiresAt: time.Now().Add(-time.Minute).UTC(), DurationSec: 60}
	if err := s.CreateInstance(in2); err != nil {
		t.Fatalf("CreateInstance 2: %v", err)
	}
	in3 := &model.Instance{UserID: u.ID, Provider: "alicloud", Status: model.StatusCreating,
		ExpiresAt: time.Now().Add(time.Hour).UTC()}
	if err := s.CreateInstance(in3); err != nil {
		t.Fatalf("CreateInstance 3: %v", err)
	}
	running, err := s.ListInstancesByStatuses([]model.InstanceStatus{model.StatusRunning})
	if err != nil || len(running) != 2 {
		t.Errorf("ListInstancesByStatuses(running): %v len=%d", err, len(running))
	}
	mixed, err := s.ListInstancesByStatuses([]model.InstanceStatus{model.StatusRunning, model.StatusCreating})
	if err != nil || len(mixed) != 3 {
		t.Errorf("ListInstancesByStatuses(mixed): %v len=%d", err, len(mixed))
	}
	// 按过期时间排序：最早过期在前（调度器扫描顺序）
	if running[0].ID != in2.ID {
		t.Errorf("expired-soonest instance should come first, got id=%d", running[0].ID)
	}
	if _, err := s.ListInstancesByStatuses(nil); err == nil {
		t.Error("empty statuses should fail")
	}

	// 按用户查询
	mine, err := s.ListInstancesByUser(u.ID)
	if err != nil || len(mine) != 3 {
		t.Errorf("ListInstancesByUser: %v len=%d", err, len(mine))
	}
	other := mustUser(t, s, "other2")
	if mine, _ := s.ListInstancesByUser(other.ID); len(mine) != 0 {
		t.Errorf("ListInstancesByUser other: len=%d", len(mine))
	}

	// 更新不存在 / 校验
	if err := s.UpdateInstance(&model.Instance{ID: 9999, Status: model.StatusRunning}); !errors.Is(err, ErrNotFound) {
		t.Errorf("update missing instance: err = %v", err)
	}
	if err := s.CreateInstance(&model.Instance{UserID: u.ID, Status: model.StatusRunning}); err == nil {
		t.Error("empty provider should fail")
	}
}

func TestAuditLog(t *testing.T) {
	s := newTestStore(t)
	u := mustUser(t, s, "auditor")

	for i := 0; i < 5; i++ {
		if err := s.CreateAuditLog(&model.AuditLog{UserID: u.ID, Action: "login", Detail: "ok"}); err != nil {
			t.Fatalf("CreateAuditLog: %v", err)
		}
	}
	logs, err := s.ListAuditLogs(3)
	if err != nil || len(logs) != 3 {
		t.Errorf("ListAuditLogs(3): %v len=%d", err, len(logs))
	}
	// 默认/越界 limit
	if logs, _ := s.ListAuditLogs(0); len(logs) != 5 {
		t.Errorf("ListAuditLogs(0) should default, len=%d", len(logs))
	}
	if logs, _ := s.ListAuditLogs(100000); len(logs) != 5 {
		t.Errorf("ListAuditLogs(100000) should cap, len=%d", len(logs))
	}
	// 倒序：最新在前
	if logs[0].ID <= logs[len(logs)-1].ID {
		t.Errorf("audit logs should be newest-first: %d vs %d", logs[0].ID, logs[len(logs)-1].ID)
	}
	if err := s.CreateAuditLog(&model.AuditLog{UserID: u.ID, Action: ""}); err == nil {
		t.Error("empty action should fail")
	}
}

func TestConcurrency(t *testing.T) {
	s := newTestStore(t)
	u := mustUser(t, s, "concurrent")
	const n = 20
	done := make(chan error, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			done <- s.CreateAuditLog(&model.AuditLog{UserID: u.ID, Action: "op", Detail: "x"})
		}(i)
	}
	for i := 0; i < n; i++ {
		if err := <-done; err != nil {
			t.Fatalf("concurrent write %d: %v", i, err)
		}
	}
	logs, err := s.ListAuditLogs(1000)
	if err != nil || len(logs) != n {
		t.Errorf("after concurrent writes: %v len=%d", err, len(logs))
	}
}

func TestCanTransition(t *testing.T) {
	tests := []struct {
		from, to model.InstanceStatus
		want     bool
	}{
		{model.StatusCreating, model.StatusRunning, true},
		{model.StatusCreating, model.StatusFailed, true},
		{model.StatusCreating, model.StatusDestroyed, false},
		{model.StatusRunning, model.StatusDestroying, true},
		{model.StatusRunning, model.StatusCreating, false},
		{model.StatusDestroying, model.StatusDestroyed, true},
		{model.StatusDestroying, model.StatusDestroying, true}, // 重试
		{model.StatusDestroyed, model.StatusRunning, false},    // 终态
		{model.StatusFailed, model.StatusDestroying, true},
		{model.StatusFailed, model.StatusRunning, false},
	}
	for _, tt := range tests {
		if got := model.CanTransition(tt.from, tt.to); got != tt.want {
			t.Errorf("CanTransition(%s, %s) = %v, want %v", tt.from, tt.to, got, tt.want)
		}
	}
}

func sha256Hex(s string) string {
	sum := sha256Sum([]byte(s))
	return hexEncode(sum)
}
