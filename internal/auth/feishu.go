package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// 飞书开放平台端点。
const (
	FeishuAuthPage    = "https://open.feishu.cn/open-apis/authen/v1/index"
	FeishuTokenURL    = "https://open.feishu.cn/open-apis/authen/v2/oauth/token"
	FeishuUserInfoURL = "https://open.feishu.cn/open-apis/authen/v1/user_info"
)

// FeishuUserInfo 是从飞书拉取的用户身份。
type FeishuUserInfo struct {
	OpenID string `json:"open_id"`
	Name   string `json:"name"`
	Email  string `json:"email"`
}

// FeishuOAuth 封装飞书登录流程；Client 可注入（测试用 httptest 服务器）。
type FeishuOAuth struct {
	AppID       string
	AppSecret   string
	RedirectURL string
	// BaseURL 覆盖默认域名（测试注入 fake 服务器），留空用官方端点。
	BaseURL string
	Client  *http.Client
}

func (f *FeishuOAuth) client() *http.Client {
	if f.Client != nil {
		return f.Client
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// AuthCodeURL 构造飞书授权跳转地址。
func (f *FeishuOAuth) AuthCodeURL(state string) string {
	q := url.Values{}
	q.Set("app_id", f.AppID)
	q.Set("redirect_uri", f.RedirectURL)
	q.Set("state", state)
	return f.authPage() + "?" + q.Encode()
}

func (f *FeishuOAuth) authPage() string {
	if f.BaseURL != "" {
		return strings.TrimSuffix(f.BaseURL, "/") + "/authen/v1/index"
	}
	return FeishuAuthPage
}

func (f *FeishuOAuth) endpoint(defaultURL, path string) string {
	if f.BaseURL != "" {
		return strings.TrimSuffix(f.BaseURL, "/") + path
	}
	return defaultURL
}

// Callback 用授权码换取 user_access_token 并拉取用户信息。
func (f *FeishuOAuth) Callback(ctx context.Context, code string) (*FeishuUserInfo, error) {
	accessToken, err := f.exchangeCode(ctx, code)
	if err != nil {
		return nil, err
	}
	return f.fetchUserInfo(ctx, accessToken)
}

type feishuTokenResp struct {
	Code        int    `json:"code"`
	Msg         string `json:"msg"`
	AccessToken string `json:"access_token"`
}

func (f *FeishuOAuth) exchangeCode(ctx context.Context, code string) (string, error) {
	body, _ := json.Marshal(map[string]string{
		"grant_type": "authorization_code",
		"client_id":  f.AppID,
		"app_id":     f.AppID,
		"app_secret": f.AppSecret,
		"code":       code,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		f.endpoint(FeishuTokenURL, "/authen/v2/oauth/token"), bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := f.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("feishu: exchange code: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("feishu: exchange code: http %d: %s", resp.StatusCode, truncate(raw))
	}
	var tr feishuTokenResp
	if err := json.Unmarshal(raw, &tr); err != nil {
		return "", fmt.Errorf("feishu: decode token resp: %w", err)
	}
	if tr.Code != 0 {
		return "", fmt.Errorf("feishu: exchange code: code=%d msg=%s", tr.Code, tr.Msg)
	}
	if tr.AccessToken == "" {
		return "", fmt.Errorf("feishu: empty access_token")
	}
	return tr.AccessToken, nil
}

type feishuUserResp struct {
	Code int            `json:"code"`
	Msg  string         `json:"msg"`
	Data FeishuUserInfo `json:"data"`
}

func (f *FeishuOAuth) fetchUserInfo(ctx context.Context, accessToken string) (*FeishuUserInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		f.endpoint(FeishuUserInfoURL, "/authen/v1/user_info"), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := f.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("feishu: fetch user info: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("feishu: fetch user info: http %d: %s", resp.StatusCode, truncate(raw))
	}
	var ur feishuUserResp
	if err := json.Unmarshal(raw, &ur); err != nil {
		return nil, fmt.Errorf("feishu: decode user resp: %w", err)
	}
	if ur.Code != 0 {
		return nil, fmt.Errorf("feishu: fetch user info: code=%d msg=%s", ur.Code, ur.Msg)
	}
	if ur.Data.OpenID == "" {
		return nil, fmt.Errorf("feishu: empty open_id")
	}
	return &ur.Data, nil
}

func truncate(b []byte) string {
	const max = 200
	if len(b) > max {
		b = b[:max]
	}
	return string(b)
}
