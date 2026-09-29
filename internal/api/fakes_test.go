package api

import (
	"context"
	"log"
	"sync"
	"testing"
	"time"

	"github.com/linuxsuren/open-cloud-web/internal/auth"
	"github.com/linuxsuren/open-cloud-web/internal/model"
)

// fakeStore 是 Store 的内存实现（仅测试）。
type fakeStore struct {
	mu        sync.Mutex
	nextID    int64
	users     map[int64]*auth.User
	byName    map[string]int64
	byPatHash map[string]*auth.PAT
	pats      map[int64]*auth.PAT
	patOwner  map[int64]int64
	instances map[int64]*Instance
	accounts  map[int64]*model.CloudAccount
	audit     []*AuditLog
	hashes    map[int64]string
	settings  map[string]string
	sgs       []*model.SecurityGroup
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		nextID: 100, users: map[int64]*auth.User{}, byName: map[string]int64{},
		byPatHash: map[string]*auth.PAT{}, pats: map[int64]*auth.PAT{}, patOwner: map[int64]int64{},
		instances: map[int64]*Instance{}, accounts: map[int64]*model.CloudAccount{}, hashes: map[int64]string{},
	}
}

func (s *fakeStore) CreateUser(u *auth.User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	u.ID = s.nextID
	cp := *u
	s.users[u.ID] = &cp
	s.byName[u.Username] = u.ID
	return nil
}

func (s *fakeStore) GetUser(id int64) (*auth.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if u, ok := s.users[id]; ok {
		cp := *u
		return &cp, nil
	}
	return nil, auth.ErrNotFound
}

func (s *fakeStore) GetUserByUsername(name string) (*auth.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id, ok := s.byName[name]; ok {
		cp := *s.users[id]
		return &cp, nil
	}
	return nil, auth.ErrNotFound
}

func (s *fakeStore) GetUserByProvider(provider, sub string) (*auth.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.users {
		if u.Provider == provider && u.ProviderSub == sub {
			cp := *u
			return &cp, nil
		}
	}
	return nil, auth.ErrNotFound
}

func (s *fakeStore) ListUsers() ([]*auth.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*auth.User
	for _, u := range s.users {
		cp := *u
		out = append(out, &cp)
	}
	return out, nil
}

func (s *fakeStore) UpdateUser(u *auth.User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[u.ID]; !ok {
		return auth.ErrNotFound
	}
	cp := *u
	s.users[u.ID] = &cp
	return nil
}

func (s *fakeStore) DeleteUser(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.users, id)
	return nil
}

func (s *fakeStore) SetUserPasswordHash(userID int64, hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hashes[userID] = hash
	return nil
}

func (s *fakeStore) CreatePAT(userID int64, name, tokenHash string, expiresAt time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	p := &auth.PAT{ID: s.nextID, UserID: userID, Name: name, TokenHash: tokenHash, ExpiresAt: expiresAt}
	s.pats[p.ID] = p
	s.byPatHash[tokenHash] = p
	s.patOwner[p.ID] = userID
	return p.ID, nil
}

func (s *fakeStore) GetPATByHash(hash string) (*auth.PAT, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.byPatHash[hash]; ok {
		cp := *p
		return &cp, nil
	}
	return nil, auth.ErrNotFound
}

func (s *fakeStore) ListPATs(userID int64) ([]*auth.PAT, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*auth.PAT
	for _, p := range s.pats {
		if p.UserID == userID {
			cp := *p
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (s *fakeStore) DeletePAT(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.pats[id]; ok {
		delete(s.byPatHash, p.TokenHash)
		delete(s.pats, id)
	}
	return nil
}

func (s *fakeStore) CreateInstance(i *Instance) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	i.ID = s.nextID
	cp := *i
	s.instances[i.ID] = &cp
	return nil
}

func (s *fakeStore) GetInstance(id int64) (*Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i, ok := s.instances[id]; ok {
		cp := *i
		return &cp, nil
	}
	return nil, auth.ErrNotFound
}

func (s *fakeStore) ListInstancesByUser(userID int64) ([]*Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*Instance
	for _, i := range s.instances {
		if i.UserID == userID {
			cp := *i
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (s *fakeStore) UpdateInstance(i *Instance) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.instances[i.ID]; !ok {
		return auth.ErrNotFound
	}
	cp := *i
	s.instances[i.ID] = &cp
	return nil
}

func (s *fakeStore) DeleteInstance(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.instances[id]; !ok {
		return auth.ErrNotFound
	}
	delete(s.instances, id)
	return nil
}

func (s *fakeStore) CreateSecurityGroup(g *model.SecurityGroup) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	g.ID = s.nextID
	cp := *g
	cp.Ports = append([]int(nil), g.Ports...)
	s.sgs = append(s.sgs, &cp)
	return nil
}

func (s *fakeStore) GetSecurityGroup(id int64) (*model.SecurityGroup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, g := range s.sgs {
		if g.ID == id {
			cp := *g
			return &cp, nil
		}
	}
	return nil, auth.ErrNotFound
}

func (s *fakeStore) ListSecurityGroups(userID int64) ([]*model.SecurityGroup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*model.SecurityGroup
	for _, g := range s.sgs {
		if g.UserID == 0 || g.UserID == userID {
			cp := *g
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (s *fakeStore) DeleteSecurityGroup(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, g := range s.sgs {
		if g.ID == id {
			s.sgs = append(s.sgs[:i], s.sgs[i+1:]...)
			return nil
		}
	}
	return auth.ErrNotFound
}

func (s *fakeStore) GetSetting(key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings[key], nil
}

func (s *fakeStore) SetSetting(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settings == nil {
		s.settings = map[string]string{}
	}
	s.settings[key] = value
	return nil
}

func (s *fakeStore) CreateAuditLog(a *AuditLog) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audit = append(s.audit, a)
	return nil
}

func (s *fakeStore) CountAuditLogs() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return int64(len(s.audit)), nil
}

func (s *fakeStore) ListAuditLogs(limit, offset int) ([]*AuditLog, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// fake 追加顺序即 id 升序，倒序分页返回。
	var desc []*AuditLog
	for i := len(s.audit) - 1; i >= 0; i-- {
		desc = append(desc, s.audit[i])
	}
	if offset >= len(desc) {
		return []*AuditLog{}, nil
	}
	desc = desc[offset:]
	if len(desc) > limit {
		desc = desc[:limit]
	}
	return append([]*AuditLog(nil), desc...), nil
}

// ---------- cloud account ----------

func (s *fakeStore) CreateCloudAccount(a *model.CloudAccount) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	a.ID = s.nextID
	cp := *a
	s.accounts[a.ID] = &cp
	return nil
}

func (s *fakeStore) GetCloudAccount(id int64) (*model.CloudAccount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a, ok := s.accounts[id]; ok {
		cp := *a
		return &cp, nil
	}
	return nil, auth.ErrNotFound
}

func (s *fakeStore) ListCloudAccountsByUser(userID int64) ([]*model.CloudAccount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*model.CloudAccount
	for _, a := range s.accounts {
		if a.UserID == userID {
			cp := *a
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (s *fakeStore) ListCloudAccounts() ([]*model.CloudAccount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*model.CloudAccount
	for _, a := range s.accounts {
		cp := *a
		out = append(out, &cp)
	}
	return out, nil
}

func (s *fakeStore) UpdateCloudAccount(a *model.CloudAccount) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.accounts[a.ID]; !ok {
		return auth.ErrNotFound
	}
	cp := *a
	s.accounts[a.ID] = &cp
	return nil
}

func (s *fakeStore) DeleteCloudAccount(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.accounts[id]; !ok {
		return auth.ErrNotFound
	}
	delete(s.accounts, id)
	return nil
}

func (s *fakeStore) getInstance(id int64) *Instance {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i, ok := s.instances[id]; ok {
		cp := *i
		return &cp
	}
	return nil
}

// fakeRunner 记录 tofu 调用，可用于断言 Apply/Destroy 是否被触发。
type fakeRunner struct {
	mu         sync.Mutex
	applied    []string
	destroys   []string
	fail       bool
	applyBlock chan struct{} // 非 nil 时 Apply 阻塞直到关闭
	applyCh    chan struct{} // 每次 Apply 完成通知
	destCh     chan struct{}
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{applyCh: make(chan struct{}, 16), destCh: make(chan struct{}, 16)}
}

func (f *fakeRunner) Apply(ctx context.Context, ws string, vars map[string]string) error {
	f.mu.Lock()
	f.applied = append(f.applied, ws)
	fail, block := f.fail, f.applyBlock
	f.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if !fail {
		f.applyCh <- struct{}{}
	}
	return nil
}

func (f *fakeRunner) Destroy(ctx context.Context, ws string) error {
	f.mu.Lock()
	f.destroys = append(f.destroys, ws)
	f.mu.Unlock()
	f.destCh <- struct{}{}
	return nil
}

func (f *fakeRunner) OutputIP(ctx context.Context, ws string) (string, string, error) {
	return "1.2.3.4", "10.0.0.4", nil
}

// fakeRunnerFactory 按账号凭据返回 fake runner（记录凭据供断言）。
func fakeRunnerFactory(runner *fakeRunner) RunnerFactory {
	return func(provider, ak, sk, session string, onLog func(string)) Runner { return runner }
}

// newTestServer 组装完整 API（内存 store + fake tofu/cloud）。
func newTestServer(t *testing.T) (*fakeStore, *fakeRunner, *Handler) {
	t.Helper()
	store := newFakeStore()
	// 预置一个安全组（模拟 seedSecurityGroups）。
	_ = store.CreateSecurityGroup(&model.SecurityGroup{UserID: 0, Name: "preset", Ports: []int{22, 80, 443}})
	runner := newFakeRunner()
	h := NewHandler(store, auth.NewManager("test-secret-0000000000000"), nil, auth.NewStateManager("state-secret-0000000"), fakeRunnerFactory(runner), Config{
		DefaultDurationSec: 3600,
		MaxDurationSec:     7200,
		DefaultRenewTimes:  3,
		CORSAllowedOrigin:  "*",
		SecretKey:          "test-enc-secret",
	})
	h.Log = log.New(log.Writer(), "", 0)
	return store, runner, h
}

// seedUser 写入用户并返回其 JWT。
func seedUser(t *testing.T, store *fakeStore, h *Handler, role auth.Role, maxDur int64) string {
	t.Helper()
	u := &auth.User{ID: 7, Username: "u7", Role: role, Status: auth.StatusActive, MaxDurationSec: maxDur, Provider: "local"}
	store.users[7] = u
	tok, err := h.Auth.IssueJWT(u)
	if err != nil {
		t.Fatalf("issue jwt: %v", err)
	}
	return tok
}
