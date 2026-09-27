package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// ---- fakes ----

type fakeStore struct {
	mu        sync.Mutex
	instances map[int64]*Instance
	updates   int
}

func newFakeStore(insts ...*Instance) *fakeStore {
	m := make(map[int64]*Instance, len(insts))
	for _, i := range insts {
		cp := *i
		m[i.ID] = &cp
	}
	return &fakeStore{instances: m}
}

func (f *fakeStore) ListInstancesByStatuses(statuses []InstanceStatus) ([]*Instance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*Instance
	for _, i := range f.instances {
		for _, st := range statuses {
			if i.Status == st {
				cp := *i
				out = append(out, &cp)
				break
			}
		}
	}
	return out, nil
}

func (f *fakeStore) UpdateInstance(inst *Instance) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates++
	if _, ok := f.instances[inst.ID]; !ok {
		return errors.New("not found")
	}
	cp := *inst
	f.instances[inst.ID] = &cp
	return nil
}

func (f *fakeStore) get(id int64) *Instance {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i, ok := f.instances[id]; ok {
		cp := *i
		return &cp
	}
	return nil
}

type fakeRunner struct {
	mu       sync.Mutex
	failures int // 前 N 次返回错误
	calls    []string
	block    chan struct{} // 非 nil 时每次 Destroy 阻塞直到关闭
	callsMu  sync.Mutex
}

func (r *fakeRunner) Destroy(_ context.Context, workspace string) error {
	r.callsMu.Lock()
	r.calls = append(r.calls, workspace)
	r.callsMu.Unlock()
	if r.block != nil {
		<-r.block
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failures > 0 {
		r.failures--
		return errors.New("tofu destroy boom")
	}
	return nil
}

func (r *fakeRunner) callCount() int {
	r.callsMu.Lock()
	defer r.callsMu.Unlock()
	return len(r.calls)
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type auditRecord struct {
	userID int64
	action string
	detail string
}

type fakeAudit struct {
	mu   sync.Mutex
	logs []auditRecord
}

func (a *fakeAudit) fn(userID int64, action, detail string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.logs = append(a.logs, auditRecord{userID, action, detail})
}

func (a *fakeAudit) actions() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for _, l := range a.logs {
		out = append(out, l.action)
	}
	return out
}

// ---- helpers ----

var base = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

func newTestScheduler(st InstanceStore, run Destroyer, aud AuditFunc, clk *fakeClock, cfg Config) *Scheduler {
	return New(st, run, aud, WithNow(clk.Now), WithConfig(cfg), WithLogger(func(string, ...any) {}))
}

func waitStatus(t *testing.T, st *fakeStore, id int64, want InstanceStatus, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if i := st.get(id); i != nil && i.Status == want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("instance %d did not reach status %q", id, want)
}

func testConfig() Config {
	return Config{
		Interval:               50 * time.Millisecond,
		CreatingTimeout:        15 * time.Minute,
		DestroyingStuckTimeout: 15 * time.Minute,
		BackoffBase:            30 * time.Second,
		MaxBackoff:             10 * time.Minute,
	}
}

// ---- tests ----

// 过期 Running 实例被销毁，审计按顺序记录 destroying → destroyed。
func TestExpiredRunningDestroyed(t *testing.T) {
	clk := &fakeClock{now: base}
	aud := &fakeAudit{}
	st := newFakeStore(&Instance{
		ID: 1, UserID: 7, Status: StatusRunning,
		TfWorkspace: "ws-1", ExpiresAt: base.Add(-time.Minute),
		CreatedAt: base.Add(-time.Hour), UpdatedAt: base.Add(-time.Hour),
	})
	run := &fakeRunner{}
	s := newTestScheduler(st, run, aud.fn, clk, testConfig())

	s.scanOnce(context.Background())
	s.wg.Wait()

	got := st.get(1)
	if got.Status != StatusDestroyed {
		t.Fatalf("status = %q, want destroyed", got.Status)
	}
	if got.ErrorMessage != "" {
		t.Fatalf("unexpected error message: %q", got.ErrorMessage)
	}
	if run.callCount() != 1 {
		t.Fatalf("destroy calls = %d, want 1", run.callCount())
	}
	acts := aud.actions()
	if len(acts) != 2 || acts[0] != "instance.destroying" || acts[1] != "instance.destroyed" {
		t.Fatalf("audit actions = %v, want [instance.destroying instance.destroyed]", acts)
	}
}

// 未过期（含刚好等于 ExpiresAt）的 Running 实例绝不能被销毁。
func TestNotExpiredNotDestroyed(t *testing.T) {
	clk := &fakeClock{now: base}
	st := newFakeStore(
		&Instance{
			ID: 1, UserID: 7, Status: StatusRunning,
			TfWorkspace: "ws-1", ExpiresAt: base.Add(time.Hour),
			CreatedAt: base, UpdatedAt: base,
		},
		&Instance{ // 恰好到达过期时刻：now == ExpiresAt 不算过期（需要 now > ExpiresAt）
			ID: 2, UserID: 8, Status: StatusRunning,
			TfWorkspace: "ws-2", ExpiresAt: base,
			CreatedAt: base, UpdatedAt: base,
		},
	)
	run := &fakeRunner{}
	s := newTestScheduler(st, run, nil, clk, testConfig())

	s.scanOnce(context.Background())
	s.wg.Wait()

	for _, id := range []int64{1, 2} {
		if got := st.get(id); got.Status != StatusRunning {
			t.Fatalf("instance %d status = %q, want running", id, got.Status)
		}
	}
	if run.callCount() != 0 {
		t.Fatalf("destroy calls = %d, want 0", run.callCount())
	}
}

// 销毁失败：保持 Destroying、记录 ErrorMessage、退避窗口内不重试、窗口过后重试成功。
func TestDestroyFailureBackoffRetry(t *testing.T) {
	clk := &fakeClock{now: base}
	st := newFakeStore(&Instance{
		ID: 1, UserID: 7, Status: StatusRunning,
		TfWorkspace: "ws-1", ExpiresAt: base.Add(-time.Minute),
		CreatedAt: base.Add(-time.Hour), UpdatedAt: base.Add(-time.Hour),
	})
	run := &fakeRunner{failures: 1}
	cfg := testConfig()
	cfg.BackoffBase = 30 * time.Second
	s := newTestScheduler(st, run, nil, clk, cfg)

	// 第一次尝试：失败。
	s.scanOnce(context.Background())
	s.wg.Wait()
	got := st.get(1)
	if got.Status != StatusDestroying {
		t.Fatalf("status after failure = %q, want destroying", got.Status)
	}
	if got.ErrorMessage == "" {
		t.Fatal("ErrorMessage not recorded after failure")
	}
	if run.callCount() != 1 {
		t.Fatalf("destroy calls = %d, want 1", run.callCount())
	}

	// 退避窗口内（now+10s < base+30s）：不得重试。
	clk.Advance(10 * time.Second)
	s.scanOnce(context.Background())
	s.wg.Wait()
	if run.callCount() != 1 {
		t.Fatalf("destroy retried inside backoff window: calls = %d", run.callCount())
	}

	// 越过退避窗口：重试，本次成功 → Destroyed。
	clk.Advance(25 * time.Second)
	s.scanOnce(context.Background())
	s.wg.Wait()
	waitStatus(t, st, 1, StatusDestroyed, time.Second)
	if run.callCount() != 2 {
		t.Fatalf("destroy calls = %d, want 2", run.callCount())
	}
}

// 退避指数增长且封顶 10min。
func TestBackoffExponentialCapped(t *testing.T) {
	s := New(newFakeStore(), &fakeRunner{}, nil, WithLogger(func(string, ...any) {}))
	want := []time.Duration{30 * time.Second, 60 * time.Second, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 10 * time.Minute, 10 * time.Minute}
	for i, w := range want {
		if got := s.recordFailure(42); got != w {
			t.Fatalf("backoff #%d = %s, want %s", i+1, got, w)
		}
	}
}

// Creating 超时 → Failed，写审计。
func TestCreatingTimeoutFailed(t *testing.T) {
	clk := &fakeClock{now: base}
	aud := &fakeAudit{}
	st := newFakeStore(
		&Instance{ // 超时
			ID: 1, UserID: 7, Status: StatusCreating,
			CreatedAt: base.Add(-16 * time.Minute), UpdatedAt: base.Add(-16 * time.Minute),
		},
		&Instance{ // 未超时
			ID: 2, UserID: 8, Status: StatusCreating,
			CreatedAt: base.Add(-time.Minute), UpdatedAt: base.Add(-time.Minute),
		},
	)
	run := &fakeRunner{}
	s := newTestScheduler(st, run, aud.fn, clk, testConfig())

	s.scanOnce(context.Background())
	s.wg.Wait()

	got := st.get(1)
	if got.Status != StatusFailed {
		t.Fatalf("status = %q, want failed", got.Status)
	}
	if got.ErrorMessage == "" {
		t.Fatal("ErrorMessage not set on creating timeout")
	}
	if got := st.get(2); got.Status != StatusCreating {
		t.Fatalf("instance 2 status = %q, want creating", got.Status)
	}
	if run.callCount() != 0 {
		t.Fatalf("destroy must not be called for creating instances, got %d calls", run.callCount())
	}
	if len(aud.logs) != 1 || aud.logs[0].action != "instance.failed" {
		t.Fatalf("audit = %+v, want one instance.failed", aud.logs)
	}
}

// Destroying 卡死（无进展超过 15min 且无失败记录）也会重试 destroy。
func TestDestroyingStuckRetried(t *testing.T) {
	clk := &fakeClock{now: base}
	st := newFakeStore(&Instance{
		ID: 1, UserID: 7, Status: StatusDestroying,
		TfWorkspace: "ws-1",
		CreatedAt:   base.Add(-time.Hour), UpdatedAt: base.Add(-16 * time.Minute),
	})
	run := &fakeRunner{}
	s := newTestScheduler(st, run, nil, clk, testConfig())

	s.scanOnce(context.Background())
	s.wg.Wait()
	waitStatus(t, st, 1, StatusDestroyed, time.Second)
	if run.callCount() != 1 {
		t.Fatalf("destroy calls = %d, want 1", run.callCount())
	}
}

// Destroying 未卡死（且无失败历史）不重复发起 destroy。
func TestDestroyingFreshNotRetried(t *testing.T) {
	clk := &fakeClock{now: base}
	st := newFakeStore(&Instance{
		ID: 1, UserID: 7, Status: StatusDestroying,
		TfWorkspace: "ws-1",
		CreatedAt:   base.Add(-time.Hour), UpdatedAt: base.Add(-time.Minute),
	})
	run := &fakeRunner{}
	s := newTestScheduler(st, run, nil, clk, testConfig())

	s.scanOnce(context.Background())
	s.wg.Wait()
	if run.callCount() != 0 {
		t.Fatalf("destroy calls = %d, want 0", run.callCount())
	}
	if got := st.get(1); got.Status != StatusDestroying {
		t.Fatalf("status = %q, want destroying", got.Status)
	}
}

// 优雅退出：context cancel 后 Run 等待在途 destroy 完成才返回。
func TestGracefulShutdownWaitsInFlightDestroy(t *testing.T) {
	clk := &fakeClock{now: base}
	st := newFakeStore(&Instance{
		ID: 1, UserID: 7, Status: StatusRunning,
		TfWorkspace: "ws-1", ExpiresAt: base.Add(-time.Minute),
		CreatedAt: base.Add(-time.Hour), UpdatedAt: base.Add(-time.Hour),
	})
	run := &fakeRunner{block: make(chan struct{})}
	cfg := testConfig()
	cfg.Interval = 10 * time.Millisecond
	s := newTestScheduler(st, run, nil, clk, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()

	waitStatus(t, st, 1, StatusDestroying, time.Second)
	cancel() // 在途 destroy 仍阻塞

	select {
	case <-done:
		t.Fatal("Run returned before in-flight destroy finished")
	case <-time.After(50 * time.Millisecond):
	}

	close(run.block) // 放行 destroy
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after in-flight destroy finished")
	}
	waitStatus(t, st, 1, StatusDestroyed, time.Second)
}
