// 华为云 Provider：ECS/IMS/IAM 目录查询。
// 签名 SDK-HMAC-SHA256，实现与官方 SDK（huaweicloud-sdk-go-v3
// core/auth/signer/signer.go）逐行对照：StringToSign =
// "SDK-HMAC-SHA256\n{X-Sdk-Date}\n{sha256hex(canonicalRequest)}"，
// 签名密钥为原始 SK；Authorization: SDK-HMAC-SHA256 Access=AK, SignedHeaders=..., Signature=...。
//
// 华为云 ECS/IMS 接口路径需要项目 ID（project_id），本实现先经
// IAM GET /v3/projects?name={region} 解析并缓存。
//
// API 文档：
//   - 项目列表: https://support.huaweicloud.com/api-iam/iam_06_0001.html
//   - 区域列表: https://support.huaweicloud.com/api-iam/iam_05_0001.html
//   - 可用区:   ECS GET /v2.1/{project_id}/os-availability-zone
//   - 规格:     ECS GET /v1/{project_id}/cloudservers/flavors
//   - 公共镜像: IMS GET /v2/cloudimages?__imagetype=gold
package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/linuxsuren/open-cloud-web/internal/model"
)

// HuaweiCloudProvider 华为云目录查询实现。
type HuaweiCloudProvider struct {
	accessKey  string
	secretKey  string
	sessionTok string // 临时密钥 X-Security-Token（参与签名头）
	iamHost    string // IAM 端点（项目/区域解析）
	httpClient *http.Client
	now        func() time.Time

	mu       sync.Mutex
	projects map[string]string // region -> project_id 缓存

	// endpointFor 计算区域级服务端点；测试可注入替换。
	endpointFor func(service, region string) string
}

// NewHuaweiCloudProvider 构造华为云 Provider。
func NewHuaweiCloudProvider(ak, sk string) *HuaweiCloudProvider {
	p := &HuaweiCloudProvider{
		accessKey:  ak,
		secretKey:  sk,
		iamHost:    "iam.myhuaweicloud.com",
		httpClient: &http.Client{Timeout: httpClientTimeout * time.Second},
		now:        time.Now,
		projects:   map[string]string{},
	}
	p.endpointFor = func(service, region string) string {
		return service + "." + region + ".myhuaweicloud.com"
	}
	return p
}

func (p *HuaweiCloudProvider) Name() string { return "huaweicloud" }

func (p *HuaweiCloudProvider) SetProxy(proxy string) error { return setProxyOn(&p.httpClient, proxy) }

// SetSessionToken 设置临时密钥 X-Security-Token（参与签名头）。
func (p *HuaweiCloudProvider) SetSessionToken(tok string) { p.sessionTok = tok }

// projectId 解析并缓存 region 对应的项目 ID。
func (p *HuaweiCloudProvider) projectId(ctx context.Context, region string) (string, error) {
	p.mu.Lock()
	if id, ok := p.projects[region]; ok {
		p.mu.Unlock()
		return id, nil
	}
	p.mu.Unlock()
	var out struct {
		Projects []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"projects"`
	}
	if err := p.callJSON(ctx, http.MethodGet, p.iamHost,
		"/v3/projects", url.Values{"name": {region}}, &out); err != nil {
		return "", fmt.Errorf("huawei resolve project(%s): %w", region, err)
	}
	for _, pr := range out.Projects {
		if pr.Name == region && pr.ID != "" {
			p.mu.Lock()
			p.projects[region] = pr.ID
			p.mu.Unlock()
			return pr.ID, nil
		}
	}
	return "", fmt.Errorf("huawei: 未找到区域 %s 的项目 ID", region)
}

// callJSON 发起一次 SDK-HMAC-SHA256 签名 GET 调用并解析 JSON。
// host 为服务端点（如 ecs.cn-north-4.myhuaweicloud.com）。
func (p *HuaweiCloudProvider) callJSON(ctx context.Context, method, host, path string,
	query url.Values, out any) error {
	sdkDate := p.now().UTC().Format("20060102T150405Z")
	contentType := "application/json"

	// canonical query：键序、RFC3986 编码。
	var parts []string
	keys := make([]string, 0, len(query))
	for k := range query {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range query[k] {
			parts = append(parts, percentEncodeRfc3986(k)+"="+percentEncodeRfc3986(v))
		}
	}
	canonicalQuery := strings.Join(parts, "&")

	// canonical URI：官方 SDK 会补尾部斜杠（path 以 / 起始且不以 / 结尾时追加）。
	canonicalURI := path
	if !strings.HasSuffix(canonicalURI, "/") {
		canonicalURI += "/"
	}

	// 签名头：content-type / host / x-sdk-date (+ x-security-token)。
	headers := [][2]string{
		{"content-type", contentType},
		{"host", host},
		{"x-sdk-date", sdkDate},
	}
	if p.sessionTok != "" {
		headers = append(headers, [2]string{"x-security-token", p.sessionTok})
	}
	var hb strings.Builder
	var names []string
	for _, h := range headers {
		hb.WriteString(h[0] + ":" + h[1] + "\n")
		names = append(names, h[0])
	}
	signedHeaders := strings.Join(names, ";")

	payloadHash := sha256hex("") // 全部为 GET，无请求体
	canonicalRequest := strings.Join([]string{
		method, canonicalURI, canonicalQuery,
		hb.String(), signedHeaders, payloadHash,
	}, "\n")

	sts := strings.Join([]string{
		"SDK-HMAC-SHA256", sdkDate, sha256hex(canonicalRequest),
	}, "\n")
	sig := fmt.Sprintf("%x", hmacSHA256([]byte(p.secretKey), sts))
	auth := fmt.Sprintf("SDK-HMAC-SHA256 Access=%s, SignedHeaders=%s, Signature=%s",
		p.accessKey, signedHeaders, sig)

	u := "https://" + host + path
	if len(query) > 0 {
		u += "?" + canonicalQuery
	}
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Host", host)
	req.Header.Set("X-Sdk-Date", sdkDate)
	if p.sessionTok != "" {
		req.Header.Set("X-Security-Token", p.sessionTok)
	}
	req.Header.Set("Authorization", auth)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("huawei %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("huawei %s: 读取响应: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Message string `json:"message"` // APIG 错误格式 {"message":....,"error_code":...}
			Code    string `json:"error_code"`
			Err     *struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"` // 云服务错误格式 {"error":{"code":...,"message":...}}
		}
		_ = json.Unmarshal(body, &e)
		code := e.Code
		msg := e.Message
		if e.Err != nil {
			code, msg = e.Err.Code, e.Err.Message
		}
		if code == "" && msg == "" {
			msg = strings.TrimSpace(string(body))
		}
		return &APIError{Provider: p.Name(), Code: code,
			Message: msg, StatusCode: resp.StatusCode}
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("huawei %s: 解析响应: %w", path, err)
		}
	}
	return nil
}

// ListRegions 调 IAM GET /v3/regions。
func (p *HuaweiCloudProvider) ListRegions(ctx context.Context) ([]string, error) {
	var out struct {
		Regions []struct {
			ID string `json:"id"`
		} `json:"regions"`
	}
	if err := p.callJSON(ctx, http.MethodGet, p.iamHost, "/v3/regions", nil, &out); err != nil {
		return nil, err
	}
	regions := make([]string, 0, len(out.Regions))
	for _, r := range out.Regions {
		if r.ID != "" {
			regions = append(regions, r.ID)
		}
	}
	return regions, nil
}

// ListZones 调 ECS GET /v2.1/{project_id}/os-availability-zone。
func (p *HuaweiCloudProvider) ListZones(ctx context.Context, region string) ([]string, error) {
	pid, err := p.projectId(ctx, region)
	if err != nil {
		return nil, err
	}
	var out struct {
		AvailabilityZoneInfo []struct {
			ZoneName  string `json:"zoneName"`
			ZoneState struct {
				Available bool `json:"available"`
			} `json:"zoneState"`
		} `json:"availabilityZoneInfo"`
	}
	host := p.endpointFor("ecs", region)
	if err := p.callJSON(ctx, http.MethodGet, host,
		"/v2.1/"+pid+"/os-availability-zone", nil, &out); err != nil {
		return nil, err
	}
	zones := make([]string, 0, len(out.AvailabilityZoneInfo))
	for _, z := range out.AvailabilityZoneInfo {
		if z.ZoneName != "" && z.ZoneState.Available {
			zones = append(zones, z.ZoneName)
		}
	}
	return zones, nil
}

// ListImages 调 IMS GET /v2/cloudimages?__imagetype=gold（公共镜像）。
func (p *HuaweiCloudProvider) ListImages(ctx context.Context, region string) ([]model.Image, error) {
	pid, err := p.projectId(ctx, region)
	if err != nil {
		return nil, err
	}
	var out struct {
		Images []struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			OsType    string `json:"__os_type"`
			OsVersion string `json:"__os_version"`
		} `json:"images"`
	}
	host := p.endpointFor("ims", region)
	q := url.Values{"__imagetype": {"gold"}, "limit": {strconv.Itoa(pageSize)}}
	if err := p.callJSON(ctx, http.MethodGet, host, "/v2/cloudimages", q, &out); err != nil {
		return nil, err
	}
	_ = pid
	images := make([]model.Image, 0, len(out.Images))
	for _, i := range out.Images {
		images = append(images, model.Image{
			ID: i.ID, Name: i.Name,
			Provider: p.Name(), Region: region,
			OSType: firstNonEmpty(i.OsType, i.OsVersion),
		})
	}
	return images, nil
}

// ListInstanceTypes 调 ECS GET /v1/{project_id}/cloudservers/flavors
// （OpenStack 风格：vcpus 为字符串核数，ram 单位 MB）。
func (p *HuaweiCloudProvider) ListInstanceTypes(ctx context.Context, region string) ([]model.InstanceTypeSpec, error) {
	pid, err := p.projectId(ctx, region)
	if err != nil {
		return nil, err
	}
	var out struct {
		Flavors []struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Vcpus string `json:"vcpus"`
			Ram   int    `json:"ram"` // MB
		} `json:"flavors"`
	}
	host := p.endpointFor("ecs", region)
	if err := p.callJSON(ctx, http.MethodGet, host,
		"/v1/"+pid+"/cloudservers/flavors", nil, &out); err != nil {
		return nil, err
	}
	specs := make([]model.InstanceTypeSpec, 0, len(out.Flavors))
	for _, f := range out.Flavors {
		cpu, _ := strconv.Atoi(strings.TrimSpace(f.Vcpus))
		id := firstNonEmpty(f.ID, f.Name)
		if id == "" {
			continue
		}
		specs = append(specs, model.InstanceTypeSpec{
			ID: id, CPU: cpu, MemoryMB: f.Ram,
			Provider: p.Name(), Region: region,
		})
	}
	return specs, nil
}
