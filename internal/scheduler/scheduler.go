// Package scheduler 实现实例生命周期调度的权威逻辑：
// 过期销毁、销毁失败指数退避重试、Creating 超时置 Failed、Destroying 卡死重试。
//
// 为了能独立于 internal/store、internal/tofu、internal/model 编译与单测，
// 本包定义了最小依赖的局部类型与接口（Go 结构化隐式接口）。
// cmd/ocw/main.go 中的装配代码负责将真实 store/tofu 适配到这些接口。
package scheduler

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// InstanceStatus 与 internal/model 中的 InstanceStatus 值一一对应，
// 由装配层负责映射。
type InstanceStatus string

const (
	StatusCreating   InstanceStatus = "creating"
	StatusRunning    InstanceStatus = "running"
	StatusDestroying InstanceStatus = "destroying"
	StatusDestroyed  InstanceStatus = "destroyed"
	StatusFailed     InstanceStatus = "failed"
)

// Instance 是调度器所需的 model.Instance 最小视图。
type Instance struct {
	ID           int64
	UserID       int64
	Status       InstanceStatus
	TfWorkspace  string
	ErrorMessage string
	ExpiresAt    time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// InstanceStore 是调度器依赖的存储契约子集（对应 store.Store 的同名方法）。
type InstanceStore interface {
	ListInstancesByStatuses(statuses []InstanceStatus) ([]*Instance, error)
	UpdateInstance(*Instance) error
}

// Destroyer 是 tofu.Runner 契约中调度器用到的子集。
// 签名与 ARCHITECTURE.md 中 Runner.Destroy 完全一致。
type Destroyer interface {
	Destroy(ctx context.Context, workspace string) error
}

// AuditFunc 每次状态变更时回调写审计日志。
type AuditFunc func(userID int64, action, detail string)

// Config 调度参数，全部可通过选项覆盖。
type Config struct {
	// Interval 扫描间隔，默认 30s。
	Interval time.Duration
	// CreatingTimeout Creating 状态最大停留时长，默认 15min。
	CreatingTimeout time.Duration
	// DestroyingStuckTimeout Destroying 无进展（UpdatedAt 未更新）多久后重试，默认 15min。
	DestroyingStuckTimeout time.Duration
	// BackoffBase 销毁失败后首次重试的基础退避，默认 30s。
	BackoffBase time.Duration
	// MaxBackoff 退避上限，默认 10min。
	MaxBackoff time.Duration
}

func (c *Config) fillDefaults() {
	if c.Interval <= 0 {
		c.Interval = 30 * time.Second
	}
	if c.CreatingTimeout <= 0 {
		c.CreatingTimeout = 15 * time.Minute
	}
	if c.DestroyingStuckTimeout <= 0 {
		c.DestroyingStuckTimeout = 15 * time.Minute
	}
	if c.BackoffBase <= 0 {
		c.BackoffBase = 30 * time.Second
	}
	if c.MaxBackoff <= 0 {
		c.MaxBackoff = 10 * time.Minute
	}
}

// Option 可选配置。
type Option func(*Scheduler)

// WithConfig 覆盖调度参数（零值字段仍用默认值）。
func WithConfig(cfg Config) Option {
	return func(s *Scheduler) { s.cfg = cfg }
}

// WithNow 注入时钟（测试用），默认 time.Now。
func WithNow(now func() time.Time) Option {
	if now == nil {
		now = time.Now
	}
	return func(s *Scheduler) { s.now = now }
}

// WithLogger 注入日志函数（测试可捕获输出），默认 log.Printf。
func WithLogger(fn func(format string, args ...any)) Option {
	if fn == nil {
		fn = log.Printf
	}
	return func(s *Scheduler) { s.logf = fn }
}

// Scheduler 生命周期调度器。
type Scheduler struct {
	store  InstanceStore
	runner Destroyer
	audit  AuditFunc
	cfg    Config
	now    func() time.Time
	logf   func(format string, args ...any)

	wg sync.WaitGroup

	mu        sync.Mutex
	nextRetry map[int64]time.Time // 实例 ID -> 下一次允许重试 destroy 的时间（内存退避表）
	fails     map[int64]int       // 实例 ID -> 连续失败次数
	inFlight  map[int64]bool      // 正在异步 destroy 的实例，避免重复发起
}

// New 创建调度器。audit 可为 nil（跳过审计）。
func New(store InstanceStore, runner Destroyer, audit AuditFunc, opts ...Option) *Scheduler {
	s := &Scheduler{
		store:     store,
		runner:    runner,
		audit:     audit,
		now:       time.Now,
		logf:      log.Printf,
		nextRetry: make(map[int64]time.Time),
		fails:     make(map[int64]int),
		inFlight:  make(map[int64]bool),
	}
	for _, opt := range opts {
		opt(s)
	}
	s.cfg.fillDefaults()
	return s
}

// Run 阻塞运行调度循环，直到 ctx 被取消，然后等待所有在途 destroy 完成后返回。
func (s *Scheduler) Run(ctx context.Context) {
	ticker := newTicker(s.cfg.Interval)
	defer ticker.Stop()
	s.logf("[scheduler] started, interval=%s creatingTimeout=%s", s.cfg.Interval, s.cfg.CreatingTimeout)
	s.scanOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			s.logf("[scheduler] context canceled, waiting for in-flight destroys")
			s.wg.Wait()
			s.logf("[scheduler] stopped")
			return
		case <-ticker.C:
			s.scanOnce(ctx)
		}
	}
}

// scanOnce 执行一轮扫描。
func (s *Scheduler) scanOnce(ctx context.Context) {
	now := s.now()
	instances, err := s.store.ListInstancesByStatuses([]InstanceStatus{
		StatusCreating, StatusRunning, StatusDestroying,
	})
	if err != nil {
		s.logf("[scheduler] list instances: %v", err)
		return
	}
	for _, inst := range instances {
		switch inst.Status {
		case StatusCreating:
			s.handleCreating(inst, now)
		case StatusRunning:
			s.handleRunning(inst, now)
		case StatusDestroying:
			s.handleDestroying(ctx, inst, now)
		}
	}
}

// handleCreating: Creating 超时 → Failed。
func (s *Scheduler) handleCreating(inst *Instance, now time.Time) {
	if now.Sub(inst.CreatedAt) <= s.cfg.CreatingTimeout {
		return
	}
	inst.Status = StatusFailed
	inst.ErrorMessage = fmt.Sprintf("creating timed out after %s", s.cfg.CreatingTimeout)
	inst.UpdatedAt = now
	if err := s.store.UpdateInstance(inst); err != nil {
		s.logf("[scheduler] mark instance %d failed: %v", inst.ID, err)
		return
	}
	s.auditLog(inst.UserID, "instance.failed", fmt.Sprintf("instance %d: %s", inst.ID, inst.ErrorMessage))
	s.logf("[scheduler] instance %d marked failed (creating timeout)", inst.ID)
}

// handleRunning: 过期 → Destroying + 异步 Destroy。
// 未过期的实例（包括处于续用窗口内的）绝不能被销毁——续用与否的判断在 API 层，
// 本层只认 ExpiresAt。
func (s *Scheduler) handleRunning(inst *Instance, now time.Time) {
	if !now.After(inst.ExpiresAt) {
		return
	}
	inst.Status = StatusDestroying
	inst.ErrorMessage = ""
	inst.UpdatedAt = now
	if err := s.store.UpdateInstance(inst); err != nil {
		s.logf("[scheduler] mark instance %d destroying: %v", inst.ID, err)
		return
	}
	s.auditLog(inst.UserID, "instance.destroying", fmt.Sprintf("instance %d expired at %s", inst.ID, inst.ExpiresAt.Format(time.RFC3339)))
	s.logf("[scheduler] instance %d expired, destroying", inst.ID)
	s.launchDestroy(inst, now)
}

// handleDestroying: 卡死（UpdatedAt 超过 DestroyingStuckTimeout 未更新）或
// 退避窗口已到 → 重试 Destroy。
func (s *Scheduler) handleDestroying(ctx context.Context, inst *Instance, now time.Time) {
	if ctx.Err() != nil {
		return
	}
	if !s.retryDue(inst.ID, now) {
		return
	}
	// 卡死判定：正常失败重试由退避表控制（UpdatedAt 会随 ErrorMessage 记录而刷新），
	// 这里兜底处理没有任何进展的卡死实例。
	if now.Sub(inst.UpdatedAt) <= s.cfg.DestroyingStuckTimeout && !s.hasFailedBefore(inst.ID) {
		return
	}
	s.launchDestroy(inst, now)
}

// retryDue 判断该实例当前是否允许发起（重试）destroy。
func (s *Scheduler) retryDue(id int64, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if inFlight, ok := s.inFlight[id]; ok && inFlight {
		return false
	}
	if next, ok := s.nextRetry[id]; ok && now.Before(next) {
		return false
	}
	return true
}

func (s *Scheduler) hasFailedBefore(id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fails[id] > 0
}

// launchDestroy 异步执行 tofu Destroy；成功→Destroyed，失败→记录 ErrorMessage，
// 下轮按指数退避重试（上限 MaxBackoff，内存退避表）。
func (s *Scheduler) launchDestroy(inst *Instance, now time.Time) {
	s.mu.Lock()
	if s.inFlight[inst.ID] {
		s.mu.Unlock()
		return
	}
	s.inFlight[inst.ID] = true
	s.mu.Unlock()

	s.wg.Add(1)
	go func(inst *Instance) {
		defer s.wg.Done()
		defer func() {
			s.mu.Lock()
			s.inFlight[inst.ID] = false
			s.mu.Unlock()
		}()
		// 用独立的后台 context：优雅退出时要等在途 destroy 自然完成，
		// 而不是把它连同调度循环一起砍掉。
		err := s.runner.Destroy(context.Background(), inst.TfWorkspace)
		finishedAt := s.now()
		if err == nil {
			s.mu.Lock()
			delete(s.nextRetry, inst.ID)
			delete(s.fails, inst.ID)
			s.mu.Unlock()
			inst.Status = StatusDestroyed
			inst.ErrorMessage = ""
			inst.UpdatedAt = finishedAt
			if uerr := s.store.UpdateInstance(inst); uerr != nil {
				s.logf("[scheduler] mark instance %d destroyed: %v", inst.ID, uerr)
			} else {
				s.auditLog(inst.UserID, "instance.destroyed", fmt.Sprintf("instance %d destroyed", inst.ID))
				s.logf("[scheduler] instance %d destroyed", inst.ID)
			}
			return
		}
		// 失败：保持 Destroying，记录 ErrorMessage，更新退避表。
		backoff := s.recordFailure(inst.ID)
		inst.Status = StatusDestroying // 保持 Destroying，下轮重试
		inst.ErrorMessage = fmt.Sprintf("destroy failed: %v (retry in ~%s)", err, backoff)
		inst.UpdatedAt = finishedAt
		if uerr := s.store.UpdateInstance(inst); uerr != nil {
			s.logf("[scheduler] record destroy failure for instance %d: %v", inst.ID, uerr)
		}
		s.logf("[scheduler] destroy instance %d failed: %v, retry in ~%s", inst.ID, err, backoff)
	}(inst)
}

// recordFailure 记录一次失败并返回退避时长：base * 2^(fails-1)，上限 MaxBackoff。
func (s *Scheduler) recordFailure(id int64) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fails[id]++
	backoff := s.cfg.BackoffBase
	for i := 1; i < s.fails[id]; i++ {
		backoff *= 2
		if backoff >= s.cfg.MaxBackoff {
			backoff = s.cfg.MaxBackoff
			break
		}
	}
	if backoff > s.cfg.MaxBackoff {
		backoff = s.cfg.MaxBackoff
	}
	s.nextRetry[id] = s.now().Add(backoff)
	return backoff
}

func (s *Scheduler) auditLog(userID int64, action, detail string) {
	if s.audit == nil {
		return
	}
	s.audit(userID, action, detail)
}

// newTicker 可在测试中替换为假 ticker；默认使用标准库 time.NewTicker。
var newTicker = time.NewTicker
