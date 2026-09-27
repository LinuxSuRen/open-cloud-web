package cloud

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/linuxsuren/open-cloud-web/internal/model"
)

// VolcengineProvider 基于火山引擎 OpenAPI（AWS SigV4 风格签名，
// HMAC-SHA256，service "ecs"）实现，纯标准库，不引入 SDK。
//
// 签名算法与 API 文档：
//   - 签名机制: https://www.volcengine.com/docs/6369/67161
//   - API 列表（计算）: https://www.volcengine.com/docs/6396/74657
type VolcengineProvider struct {
	accessKey string
	secretKey string
	// host API 端点，默认 open.volcengineapi.com（测试可替换）。
	host string
	// region 签名与请求使用的 region，默认 cn-north-1。
	region string
	// service 签名服务名（火山引擎 OpenAPI 路由按 Credential scope 的 service 定位，ECS 为 "ecs"）。
	service string
	// apiVersion X-Version 头（compute 2020-04-01）。
	apiVersion string

	httpClient *http.Client
	now        func() time.Time
}

// NewVolcengineProvider 用注入的火山引擎凭证构造 Provider。
func NewVolcengineProvider(ak, sk string) *VolcengineProvider {
	return &VolcengineProvider{
		accessKey:  ak,
		secretKey:  sk,
		host:       "open.volcengineapi.com",
		region:     "cn-north-1",
		service:    "ecs",
		apiVersion: "2020-04-01",
		httpClient: &http.Client{Timeout: httpClientTimeout * time.Second},
		now:        time.Now,
	}
}

func (p *VolcengineProvider) Name() string { return "volcengine" }

// ListImages 调用 DescribeImages（公共镜像）。
// https://www.volcengine.com/docs/6396/76324
func (p *VolcengineProvider) ListImages(ctx context.Context, region string) ([]model.Image, error) {
	body, err := p.callOpenAPI(ctx, "DescribeImages", url.Values{
		"PageSize":   {strconv.Itoa(pageSize)},
		"PageNumber": {"1"},
		"Visibility": {"public"},
	})
	if err != nil {
		return nil, err
	}
	raws := firstList(body, []string{"Result", "Images"}, []string{"Images"})

	images := make([]model.Image, 0, len(raws))
	for _, raw := range raws {
		var m map[string]any
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		images = append(images, model.Image{
			ID:          str(m, "ImageID", "Id", "ImageId"),
			Name:        str(m, "Name", "ImageName"),
			Description: str(m, "Description"),
			OSType:      str(m, "OSName", "OSType", "Platform"),
			Provider:    p.Name(), Region: region,
		})
	}
	return images, nil
}

// ListInstanceTypes 调用 DescribeInstanceTypes。
// https://www.volcengine.com/docs/6396/76330
func (p *VolcengineProvider) ListInstanceTypes(ctx context.Context, region string) ([]model.InstanceTypeSpec, error) {
	body, err := p.callOpenAPI(ctx, "DescribeInstanceTypes", url.Values{
		"PageSize":   {strconv.Itoa(pageSize)},
		"PageNumber": {"1"},
	})
	if err != nil {
		return nil, err
	}
	raws := firstList(body, []string{"Result", "InstanceTypes"}, []string{"InstanceTypes"})
	specs := make([]model.InstanceTypeSpec, 0, len(raws))
	for _, raw := range raws {
		var m map[string]any
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		cpu := int(num(m, "CPU.CoreCount", "CpuCount", "CoreCount"))
		memGB := num(m, "Memory.Size", "MemorySize", "Size")
		specs = append(specs, model.InstanceTypeSpec{
			ID:  str(m, "InstanceTypeId", "InstanceTypeID", "Id"),
			CPU: cpu, MemoryMB: int(memGB * 1024), // GB -> MB
			Provider: p.Name(), Region: region,
		})
	}
	return specs, nil
}

// ListRegions 调用 DescribeZones（火山引擎按可用区枚举，作为地域/zone 选择依据）。
// https://www.volcengine.com/docs/6396/76328
func (p *VolcengineProvider) ListRegions(ctx context.Context) ([]string, error) {
	body, err := p.callOpenAPI(ctx, "DescribeZones", url.Values{})
	if err != nil {
		return nil, err
	}
	raws := firstList(body, []string{"Result", "Zones"}, []string{"Zones"})
	zones := make([]string, 0, len(raws))
	for _, raw := range raws {
		var m map[string]any
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		if id := str(m, "ZoneId", "ID", "Zone"); id != "" {
			zones = append(zones, id)
		}
	}
	return zones, nil
}

// callOpenAPI 发起一次签名 GET 调用并返回响应体。
// 非 2xx 或 ResponseMetadata.Error 携带错误码时返回 *APIError。
func (p *VolcengineProvider) callOpenAPI(ctx context.Context, action string, query url.Values) ([]byte, error) {
	amzDate := p.now().UTC().Format("20060102T150405Z")
	// 火山引擎要求 Action/Version 位于 query string（并参与规范化签名）。
	query.Set("Action", action)
	query.Set("Version", p.apiVersion)
	auth := volcengineSign(p.secretKey, p.accessKey, p.region, p.service,
		http.MethodGet, p.host, amzDate, query)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://"+p.host+"/?"+canonicalQuery(query), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Host", p.host)
	req.Header.Set("X-Date", amzDate)
	req.Header.Set("Authorization", auth)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("volcengine %s: %w", action, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("volcengine %s: 读取响应: %w", action, err)
	}
	var meta struct {
		ResponseMetadata struct {
			Error struct {
				Code    string `json:"Code"`
				CodeN   int    `json:"CodeN"`
				Message string `json:"Message"`
			} `json:"Error"`
		} `json:"ResponseMetadata"`
	}
	_ = json.Unmarshal(body, &meta)
	e := meta.ResponseMetadata.Error
	if resp.StatusCode != http.StatusOK || e.Code != "" || e.CodeN != 0 {
		code := e.Code
		if code == "" && e.CodeN != 0 {
			code = strconv.Itoa(e.CodeN)
		}
		return nil, &APIError{Provider: p.Name(), Code: code,
			Message: e.Message, StatusCode: resp.StatusCode}
	}
	return body, nil
}

// volcengineSign 计算火山引擎 OpenAPI 签名（AWS SigV4 风格，HMAC-SHA256）：
//
//	StringToSign = "HMAC-SHA256\n" + amzDate + "\n" + credentialScope + "\n" +
//	               sha256hex(canonicalRequest)
//	credentialScope = date + "/" + region + "/" + service + "/request"
//	签名密钥链 = hmac(hmac(hmac(hmac(secret, date), region), service), "request")
//
// 文档：https://www.volcengine.com/docs/6369/67161
func volcengineSign(secretKey, accessKey, region, service, httpMethod, host,
	amzDate string, query url.Values) string {
	signedHeaders := "content-type;host;x-date"
	// 每个规范头以 \n 结尾；与后续 Join 的分隔符共同构成空行。
	canonicalHeaders := "content-type:application/json\n" +
		"host:" + host + "\n" +
		"x-date:" + amzDate + "\n"
	payloadHash := sha256hex("")
	canonicalRequest := strings.Join([]string{
		httpMethod,
		"/",
		canonicalQuery(query),
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")
	date := amzDate[:8]
	scope := strings.Join([]string{date, region, service, "request"}, "/")
	stringToSign := strings.Join([]string{
		"HMAC-SHA256", amzDate, scope, sha256hex(canonicalRequest),
	}, "\n")

	kDate := hmacSHA256([]byte(secretKey), date)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	kSigning := hmacSHA256(kService, "request")
	return "HMAC-SHA256 Credential=" + accessKey + "/" + scope +
		", SignedHeaders=" + signedHeaders +
		", Signature=" + hex.EncodeToString(hmacSHA256(kSigning, stringToSign))
}

// canonicalQuery 按参数名字典序生成 RFC 3986 编码的查询串。
func canonicalQuery(query url.Values) string {
	keys := make([]string, 0, len(query))
	for k := range query {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		for _, v := range query[k] {
			parts = append(parts, percentEncodeRfc3986(k)+"="+percentEncodeRfc3986(v))
		}
	}
	return strings.Join(parts, "&")
}

func hmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	return mac.Sum(nil)
}

func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// firstList 在响应体中按候选路径（自外向内）定位第一个数组。
// 兼容 {"Result":{...}} 包裹与顶层数组两种返回形式。
func firstList(body []byte, paths ...[]string) []json.RawMessage {
	for _, path := range paths {
		var cur any = map[string]any{}
		if json.Unmarshal(body, &cur) != nil {
			continue
		}
		ok := true
		for _, key := range path {
			m, isMap := cur.(map[string]any)
			if !isMap {
				ok = false
				break
			}
			cur, ok = m[key]
			if !ok {
				break
			}
		}
		if arr, isArr := cur.([]any); ok && isArr {
			raws := make([]json.RawMessage, 0, len(arr))
			for _, item := range arr {
				raw, err := json.Marshal(item)
				if err != nil {
					continue
				}
				raws = append(raws, raw)
			}
			return raws
		}
	}
	return nil
}

// str 从松散 JSON 对象按候选 key 取第一个非空字符串。
func str(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// num 从松散 JSON 对象按候选路径取数值；支持 "a.b" 一层嵌套。
func num(m map[string]any, keys ...string) float64 {
	for _, k := range keys {
		var v any = m
		for _, seg := range strings.Split(k, ".") {
			mm, ok := v.(map[string]any)
			if !ok {
				v = nil
				break
			}
			v, ok = mm[seg]
			if !ok {
				break
			}
		}
		switch n := v.(type) {
		case float64:
			return n
		case string:
			if f, err := strconv.ParseFloat(n, 64); err == nil {
				return f
			}
		}
	}
	return 0
}
