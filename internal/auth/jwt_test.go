package auth

import (
	"strings"
	"testing"
	"time"
)

func testUser() *User {
	return &User{ID: 42, Username: "alice", Role: RoleUser, Status: StatusActive}
}

func TestIssueAndParseJWT(t *testing.T) {
	m := NewManager("0123456789abcdef0123456789abcdef")
	tok, err := m.IssueJWT(testUser())
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if tok == "" || strings.Count(tok, ".") != 2 {
		t.Fatalf("bad token shape: %q", tok)
	}
	c, err := m.ParseJWT(tok)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.UID != 42 || c.Role != RoleUser {
		t.Fatalf("claims mismatch: %+v", c)
	}
	if time.Until(c.Exp) > TokenTTL+time.Minute || time.Until(c.Exp) < TokenTTL-time.Minute {
		t.Fatalf("exp not ~2h: %v", c.Exp)
	}
}

func TestParseJWTWrongSecret(t *testing.T) {
	tok, _ := NewManager("secret-aaaaaaaaaaaaaaaaaaaa").IssueJWT(testUser())
	if _, err := NewManager("secret-bbbbbbbbbbbbbbbbbbbb").ParseJWT(tok); err == nil {
		t.Fatal("expected error for wrong secret")
	}
}

func TestParseJWTExpired(t *testing.T) {
	m := &jwtManager{secret: []byte("secret-cccccccccccccccccccc"), now: func() time.Time {
		return time.Now().Add(-3 * time.Hour)
	}}
	tok, err := m.IssueJWT(testUser())
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := NewManager("secret-cccccccccccccccccccc").ParseJWT(tok); err == nil {
		t.Fatal("expected expired token to fail")
	}
}

func TestParseJWTGarbage(t *testing.T) {
	m := NewManager("secret-dddddddddddddddddddd")
	for _, tok := range []string{"", "abc", "a.b.c", "eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0.e30."} {
		if _, err := m.ParseJWT(tok); err == nil {
			t.Fatalf("expected error for %q", tok)
		}
	}
}

func TestGeneratePAT(t *testing.T) {
	plain, hash, err := GeneratePAT()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !strings.HasPrefix(plain, PATPrefix) {
		t.Fatalf("missing prefix: %q", plain)
	}
	// 32 bytes -> 43 chars base64url, no padding.
	if len(plain) != len(PATPrefix)+43 || strings.ContainsAny(plain, "+/=") {
		t.Fatalf("bad body: %q", plain)
	}
	if hash != HashToken(plain) || len(hash) != 64 {
		t.Fatalf("bad hash: %q", hash)
	}
	p2, h2, _ := GeneratePAT()
	if p2 == plain || h2 == hash {
		t.Fatal("PATs must be unique")
	}
	if !IsPAT(plain) || IsPAT("jwt-value") || IsPAT(PATPrefix) {
		t.Fatal("IsPAT misclassifies")
	}
}

func TestStateManager(t *testing.T) {
	s := NewStateManager("state-eeeeeeeeeeeeeeeeeeeee")
	st, err := s.New()
	if err != nil {
		t.Fatalf("new state: %v", err)
	}
	if !s.Validate(st) {
		t.Fatal("valid state rejected")
	}
	for _, bad := range []string{"", "deadbeef", "deadbeef.", "deadbeef.wrongmac", "deadbeef." + strings.SplitN(st, ".", 2)[1] + "x"} {
		if s.Validate(bad) {
			t.Fatalf("invalid state accepted: %q", bad)
		}
	}
	other := NewStateManager("state-ffffffffffffffffffff")
	if other.Validate(st) {
		t.Fatal("state from another signer accepted")
	}
}
