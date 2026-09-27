// Package cloud 定义云提供商抽象（镜像/规格/地域查询）。
// 契约签名见仓库根 ARCHITECTURE.md（internal/cloud 一节）。
//
// 不引入任何云 SDK：阿里云走 RPC 签名 v1（HMAC-SHA1），
// 火山引擎走 AWS SigV4 风格签名（HMAC-SHA256，service "compute"），
// 全部基于标准库实现。
package cloud

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/linuxsuren/open-cloud-web/internal/model"
)

// Provider 云提供商查询接口（跨包契约，签名必须与 ARCHITECTURE.md
// 保持精确一致）。
type Provider interface {
	// Name 返回 provider 标识："alicloud" | "volcengine"。
	Name() string
	ListImages(ctx context.Context, region string) ([]model.Image, error)
	ListInstanceTypes(ctx context.Context, region string) ([]model.InstanceTypeSpec, error)
	ListRegions(ctx context.Context) ([]string, error)
}

// APIError 云 OpenAPI 返回的业务错误（透传 API 错误码）。
type APIError struct {
	Provider   string // "alicloud" | "volcengine"
	Code       string // 云端返回的错误码
	Message    string // 云端返回的错误信息
	StatusCode int    // HTTP 状态码（0 表示响应体层错误）
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s API 错误 %s: %s", e.Provider, e.Code, e.Message)
}

// pageSize 三个查询接口统一分页大小：只取第一页（前 100 条）。
const pageSize = 100

// httpClientTimeout 所有 OpenAPI 调用的 HTTP 客户端超时（5s）。
const httpClientTimeout = 5

// registry 全局 provider 注册表；并发安全。
var registry = struct {
	sync.RWMutex
	providers map[string]Provider
}{providers: map[string]Provider{}}

// Register 注册 provider（同名覆盖，便于测试/热替换）。
func Register(p Provider) {
	if p == nil || p.Name() == "" {
		return
	}
	registry.Lock()
	defer registry.Unlock()
	registry.providers[p.Name()] = p
}

// Get 按名获取 provider。
func Get(name string) (Provider, error) {
	registry.RLock()
	defer registry.RUnlock()
	p, ok := registry.providers[name]
	if !ok {
		return nil, fmt.Errorf("cloud: 未注册的 provider %q", name)
	}
	return p, nil
}

// Names 返回已注册 provider 名（字典序，便于 API 层枚举）。
func Names() []string {
	registry.RLock()
	defer registry.RUnlock()
	names := make([]string, 0, len(registry.providers))
	for n := range registry.providers {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// resetRegistry 仅测试用：清空注册表。
func resetRegistry() {
	registry.Lock()
	defer registry.Unlock()
	registry.providers = map[string]Provider{}
}
