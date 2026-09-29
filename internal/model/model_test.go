package model

import (
	"testing"
	"time"
)

func TestEnumsValid(t *testing.T) {
	if !RoleAdmin.Valid() || !RoleUser.Valid() || Role("x").Valid() {
		t.Error("Role.Valid misbehaving")
	}
	if !StatusActive.Valid() || !StatusDisabled.Valid() || UserStatus("x").Valid() {
		t.Error("UserStatus.Valid misbehaving")
	}
	for _, s := range []InstanceStatus{StatusCreating, StatusRunning, StatusDestroying, StatusDestroyed, StatusFailed} {
		if !s.Valid() {
			t.Errorf("%s should be valid", s)
		}
	}
	if InstanceStatus("x").Valid() {
		t.Error("bogus instance status should be invalid")
	}
}

func TestCanRenew(t *testing.T) {
	now := time.Now().UTC()
	expired := &Instance{Status: StatusRunning, ExpiresAt: now.Add(-time.Minute)}
	if expired.CanRenew(now) {
		t.Error("expired instance must not be renewable")
	}

	fresh := &Instance{Status: StatusRunning, ExpiresAt: now.Add(time.Hour)}
	if !fresh.CanRenew(now) {
		t.Error("running unexpired instance should be renewable")
	}

	renewed := now
	once := &Instance{Status: StatusRunning, ExpiresAt: now.Add(time.Hour), RenewedAt: &renewed, RenewedTimes: 3}
	if !once.CanRenew(now) {
		t.Error("已续期过的实例仍具备基础续期条件（次数配额由 API 层判断）")
	}

	creating := &Instance{Status: StatusCreating, ExpiresAt: now.Add(time.Hour)}
	if creating.CanRenew(now) {
		t.Error("creating instance must not be renewable")
	}

	var nilInst *Instance
	if nilInst.CanRenew(now) {
		t.Error("nil instance must not be renewable")
	}

	// 边界：恰好等于过期时刻不可续用
	edge := &Instance{Status: StatusRunning, ExpiresAt: now}
	if edge.CanRenew(now) {
		t.Error("instance at exact expiry must not be renewable")
	}
}

func TestCanTransition(t *testing.T) {
	legal := [][2]InstanceStatus{
		{StatusCreating, StatusRunning},
		{StatusCreating, StatusFailed},
		{StatusRunning, StatusDestroying},
		{StatusDestroying, StatusDestroyed},
	}
	for _, p := range legal {
		if !CanTransition(p[0], p[1]) {
			t.Errorf("%s -> %s should be legal", p[0], p[1])
		}
	}
	illegal := [][2]InstanceStatus{
		{StatusDestroyed, StatusRunning},
		{StatusRunning, StatusCreating},
		{StatusCreating, StatusDestroyed},
	}
	for _, p := range illegal {
		if CanTransition(p[0], p[1]) {
			t.Errorf("%s -> %s should be illegal", p[0], p[1])
		}
	}
}
