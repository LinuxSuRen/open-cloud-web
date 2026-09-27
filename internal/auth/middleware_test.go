package auth

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fakeUserStore struct {
	users map[int64]*User
	pats  map[string]*PAT
}

func (s *fakeUserStore) GetUser(id int64) (*User, error) {
	if u, ok := s.users[id]; ok {
		return u, nil
	}
	return nil, ErrNotFound
}

func (s *fakeUserStore) GetPATByHash(hash string) (*PAT, error) {
	if p, ok := s.pats[hash]; ok {
		return p, nil
	}
	return nil, ErrNotFound
}

func newAuthTestServer(t *testing.T, store *fakeUserStore) http.Handler {
	t.Helper()
	mgr := NewManager("mw-secret-00000000000000000")
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := UserFromContext(r.Context())
		if u == nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		fmt.Fprintf(w, "uid=%d role=%s", u.ID, u.Role)
	})
	return Authenticate(store, mgr)(next)
}

func TestMiddlewareJWT(t *testing.T) {
	store := &fakeUserStore{users: map[int64]*User{42: testUser()}}
	srv := newAuthTestServer(t, store)
	mgr := NewManager("mw-secret-00000000000000000")
	tok, _ := mgr.IssueJWT(testUser())

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	_ = tok
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "uid=42 role=user" {
		t.Fatalf("jwt: %d %q", rec.Code, rec.Body.String())
	}

	// unknown uid
	req.Header.Set("Authorization", "Bearer must-be-invalid")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("invalid jwt: %d", rec.Code)
	}
}

func TestMiddlewarePAT(t *testing.T) {
	plain, hash, err := GeneratePAT()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	store := &fakeUserStore{
		users: map[int64]*User{42: testUser()},
		pats:  map[string]*PAT{hash: {ID: 1, UserID: 42}},
	}
	srv := newAuthTestServer(t, store)

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+plain)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "uid=42 role=user" {
		t.Fatalf("pat: %d %q", rec.Code, rec.Body.String())
	}

	// unknown PAT
	other, _, _ := GeneratePAT()
	req.Header.Set("Authorization", "Bearer "+other)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown pat: %d", rec.Code)
	}

	// expired PAT
	store.pats[hash].ExpiresAt = time.Now().Add(-time.Minute)
	req.Header.Set("Authorization", "Bearer "+plain)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expired pat: %d", rec.Code)
	}
}

func TestMiddlewareDisabledUser(t *testing.T) {
	u := testUser()
	u.Status = StatusDisabled
	store := &fakeUserStore{users: map[int64]*User{42: u}}
	srv := newAuthTestServer(t, store)
	tok, _ := NewManager("mw-secret-00000000000000000").IssueJWT(u)

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("disabled user: %d", rec.Code)
	}
}
