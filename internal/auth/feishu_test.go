package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeFeishu 模拟 token 交换与 user_info 两个端点。
func fakeFeishu(t *testing.T, tokenCode int, userCode int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /authen/v2/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]string
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["grant_type"] != "authorization_code" || req["code"] == "" || req["app_id"] == "" || req["app_secret"] == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 99991668, "msg": "invalid request"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": tokenCode, "msg": "ok", "access_token": "u-at-123", "token_type": "Bearer",
		})
	})
	mux.HandleFunc("GET /authen/v1/user_info", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer u-at-123" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": userCode, "msg": "ok",
			"data": map[string]string{"open_id": "ou_abc", "name": "张三", "email": "zs@example.com"},
		})
	})
	return httptest.NewServer(mux)
}

func TestAuthCodeURL(t *testing.T) {
	f := &FeishuOAuth{AppID: "cli_a", RedirectURL: "https://app.example.com/cb"}
	u := f.AuthCodeURL("st")
	if !strings.HasPrefix(u, "https://open.feishu.cn/open-apis/authen/v1/index?") {
		t.Fatalf("bad prefix: %q", u)
	}
	for _, want := range []string{"app_id=cli_a", "state=st", "redirect_uri=https%3A%2F%2Fapp.example.com%2Fcb"} {
		if !strings.Contains(u, want) {
			t.Fatalf("missing %s in %q", want, u)
		}
	}
}

func TestCallbackHappyPath(t *testing.T) {
	srv := fakeFeishu(t, 0, 0)
	defer srv.Close()
	f := &FeishuOAuth{AppID: "cli_a", AppSecret: "s3cret", BaseURL: srv.URL, Client: srv.Client()}
	ui, err := f.Callback(context.Background(), "good-code")
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	if ui.OpenID != "ou_abc" || ui.Name != "张三" || ui.Email != "zs@example.com" {
		t.Fatalf("userinfo mismatch: %+v", ui)
	}
}

func TestCallbackBadCode(t *testing.T) {
	srv := fakeFeishu(t, 0, 0)
	defer srv.Close()
	f := &FeishuOAuth{AppID: "cli_a", AppSecret: "s3cret", BaseURL: srv.URL, Client: srv.Client()}
	if _, err := f.Callback(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty code")
	}
}

func TestCallbackTokenError(t *testing.T) {
	srv := fakeFeishu(t, 99991663, 0)
	defer srv.Close()
	f := &FeishuOAuth{AppID: "cli_a", AppSecret: "s3cret", BaseURL: srv.URL, Client: srv.Client()}
	_, err := f.Callback(context.Background(), "good-code")
	if err == nil || !strings.Contains(err.Error(), "99991663") {
		t.Fatalf("expected token error code, got %v", err)
	}
}

func TestCallbackUserInfoError(t *testing.T) {
	srv := fakeFeishu(t, 0, 99991672)
	defer srv.Close()
	f := &FeishuOAuth{AppID: "cli_a", AppSecret: "s3cret", BaseURL: srv.URL, Client: srv.Client()}
	_, err := f.Callback(context.Background(), "good-code")
	if err == nil || !strings.Contains(err.Error(), "99991672") {
		t.Fatalf("expected user info error code, got %v", err)
	}
}
