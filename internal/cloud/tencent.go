// 腾讯云 Provider：CVM 目录查询（地域/可用区/镜像/规格）。
// 签名 TC3-HMAC-SHA256，实现与官方 SDK（tencentcloud-sdk-go common/client.go）
// 逐行对照：canonical query 剔除 Action/Version/Region 等标准参数，
// 签名头 content-type;host，密钥链 TC3+SK → date → service → tc3_request。
//
// API 文档：
//   - DescribeZones: https://cloud.tencent.com/document/api/213/15707
//   - DescribeRegions: https://cloud.tencent.com/document/api/213/15708
//   - DescribeImages: https://cloud.tencent.com/document/api/213/15715
//   - DescribeInstanceTypeConfigs: https://cloud.tencent.com/document/api/213/15817
package cloud

import (
	"context"
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

// TencentCloudProvider 腾讯云目录查询实现。
type TencentCloudProvider struct {
	accessKey  string
	secretKey  string
	sessionTok string // 临时密钥 token（X-TC-Token 头，不参与签名）
	host       string
	httpClient *http.Client
	now        func() time.Time
}

// NewTencentCloudProvider 构造腾讯云 Provider（测试可替换 host/client/now）。
func NewTencentCloudProvider(ak, sk string) *TencentCloudProvider {
	return &TencentCloudProvider{
		accessKey:  ak,
		secretKey:  sk,
		host:       "cvm.tencentcloudapi.com",
		httpClient: &http.Client{Timeout: httpClientTimeout * time.Second},
		now:        time.Now,
	}
}

func (p *TencentCloudProvider) Name() string { return "tencentcloud" }

func (p *TencentCloudProvider) SetProxy(proxy string) error { return setProxyOn(&p.httpClient, proxy) }

// SetSessionToken 设置临时密钥 token（X-TC-Token 头，不参与 TC3 签名）。
func (p *TencentCloudProvider) SetSessionToken(tok string) { p.sessionTok = tok }

// callCVM 发起一次 TC3 签名 GET 调用。Action/Version/Region 走 URL 查询参数
// 但不计入 canonical query（与官方 SDK 一致）；业务参数同样走查询串。
func (p *TencentCloudProvider) callCVM(ctx context.Context, action, region string, bizParams url.Values) (json.RawMessage, error) {
	ts := p.now().UTC()
	unixTs := strconv.FormatInt(ts.Unix(), 10)
	date := ts.Format("2006-01-02")
	service := "cvm"
	// 腾讯云 GET 请求只接受表单 Content-Type（官方 SDK 同款）。
	contentType := "application/x-www-form-urlencoded"

	// canonical query：业务参数（剔除标准参数），键序 RFC3986 编码。
	stdKeys := map[string]bool{"Action": true, "Version": true, "Nonce": true,
		"Region": true, "RequestClient": true, "Timestamp": true, "Token": true,
		"SecretId": true, "Signature": true, "SignatureMethod": true}
	canonicalQuery := ""
	if len(bizParams) > 0 {
		var keys []string
		for k := range bizParams {
			if !stdKeys[k] {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		var parts []string
		for _, k := range keys {
			for _, v := range bizParams[k] {
				parts = append(parts, percentEncodeRfc3986(k)+"="+percentEncodeRfc3986(v))
			}
		}
		canonicalQuery = strings.Join(parts, "&")
	}

	canonicalHeaders := "content-type:" + contentType + "\nhost:" + p.host + "\n"
	payloadHash := sha256hex("")
	canonicalRequest := strings.Join([]string{
		http.MethodGet, "/", canonicalQuery,
		canonicalHeaders, "content-type;host", payloadHash,
	}, "\n")

	scope := date + "/" + service + "/tc3_request"
	stringToSign := strings.Join([]string{
		"TC3-HMAC-SHA256", unixTs, scope, sha256hex(canonicalRequest),
	}, "\n")

	kDate := hmacSHA256([]byte("TC3"+p.secretKey), date)
	kService := hmacSHA256(kDate, service)
	kSigning := hmacSHA256(kService, "tc3_request")
	sig := hex.EncodeToString(hmacSHA256(kSigning, stringToSign))

	auth := fmt.Sprintf("TC3-HMAC-SHA256 Credential=%s/%s, SignedHeaders=content-type;host, Signature=%s",
		p.accessKey, scope, sig)

	// 实际请求 URL：标准参数 + 业务参数。
	q := url.Values{}
	q.Set("Action", action)
	q.Set("Version", "2017-03-12")
	if region != "" {
		q.Set("Region", region)
	}
	for k, vs := range bizParams {
		for _, v := range vs {
			q.Add(k, v)
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://"+p.host+"/?"+encodeQueryTc(q), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Host", p.host)
	req.Header.Set("X-TC-Action", action)
	req.Header.Set("X-TC-Version", "2017-03-12")
	req.Header.Set("X-TC-Timestamp", unixTs)
	if region != "" {
		req.Header.Set("X-TC-Region", region)
	}
	if p.sessionTok != "" {
		req.Header.Set("X-TC-Token", p.sessionTok)
	}
	req.Header.Set("Authorization", auth)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tencentcloud %s: %w", action, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("tencentcloud %s: 读取响应: %w", action, err)
	}
	var envelope struct {
		Response json.RawMessage `json:"Response"`
		Error    *struct {
			Code    string `json:"Code"`
			Message string `json:"Message"`
		} `json:"Error"` // 非 2xx 时错误直接在顶层（网关），业务错误在 Response.Error
	}
	if json.Unmarshal(body, &envelope) != nil {
		return nil, fmt.Errorf("tencentcloud %s: 非法响应", action)
	}
	if envelope.Error != nil {
		return nil, &APIError{Provider: p.Name(), Code: envelope.Error.Code,
			Message: envelope.Error.Message, StatusCode: resp.StatusCode}
	}
	if len(envelope.Response) == 0 {
		return nil, fmt.Errorf("tencentcloud %s: 响应缺少 Response 字段", action)
	}
	// 业务错误：Response.Error
	var bizErr struct {
		Error *struct {
			Code    string `json:"Code"`
			Message string `json:"Message"`
		} `json:"Error"`
	}
	_ = json.Unmarshal(envelope.Response, &bizErr)
	if bizErr.Error != nil {
		return nil, &APIError{Provider: p.Name(), Code: bizErr.Error.Code,
			Message: bizErr.Error.Message, StatusCode: resp.StatusCode}
	}
	return envelope.Response, nil
}

// encodeQueryTc 按官方 SDK 的 GetUrlQueriesEncoded 风格编码（业务侧对
// Action/Version 等简单值无差异，标准库转义即可）。
func encodeQueryTc(q url.Values) string {
	return q.Encode()
}

// ListRegions 调 DescribeRegions。
func (p *TencentCloudProvider) ListRegions(ctx context.Context) ([]string, error) {
	resp, err := p.callCVM(ctx, "DescribeRegions", "", url.Values{})
	if err != nil {
		return nil, err
	}
	var out struct {
		RegionSet []struct {
			Region string `json:"Region"`
		} `json:"RegionSet"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		return nil, fmt.Errorf("tencentcloud DescribeRegions 解析: %w", err)
	}
	regions := make([]string, 0, len(out.RegionSet))
	for _, r := range out.RegionSet {
		if r.Region != "" {
			regions = append(regions, r.Region)
		}
	}
	return regions, nil
}

// ListZones 调 DescribeZones。
func (p *TencentCloudProvider) ListZones(ctx context.Context, region string) ([]string, error) {
	resp, err := p.callCVM(ctx, "DescribeZones", region, url.Values{})
	if err != nil {
		return nil, err
	}
	var out struct {
		Zones []struct {
			Zone string `json:"Zone"`
		} `json:"Zones"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		return nil, fmt.Errorf("tencentcloud DescribeZones 解析: %w", err)
	}
	zones := make([]string, 0, len(out.Zones))
	for _, z := range out.Zones {
		if z.Zone != "" {
			zones = append(zones, z.Zone)
		}
	}
	return zones, nil
}

// ListImages 调 DescribeImages（公共镜像第一页）。
func (p *TencentCloudProvider) ListImages(ctx context.Context, region string) ([]model.Image, error) {
	resp, err := p.callCVM(ctx, "DescribeImages", region, url.Values{
		"ImageType": {"PUBLIC_IMAGE"},
		"Limit":     {strconv.Itoa(pageSize)},
		"Offset":    {"0"},
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		ImageSet []struct {
			ImageID          string `json:"ImageId"`
			ImageName        string `json:"ImageName"`
			PlatformType     string `json:"PlatformType"`
			OsType           string `json:"OsType"`
			ImageDescription string `json:"ImageDescription"`
		} `json:"ImageSet"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		return nil, fmt.Errorf("tencentcloud DescribeImages 解析: %w", err)
	}
	images := make([]model.Image, 0, len(out.ImageSet))
	for _, i := range out.ImageSet {
		images = append(images, model.Image{
			ID:       i.ImageID,
			Name:     i.ImageName,
			Provider: p.Name(), Region: region,
			OSType:      firstNonEmpty(i.PlatformType, i.OsType),
			Description: i.ImageDescription,
		})
	}
	return images, nil
}

// ListInstanceTypes 调 DescribeInstanceTypeConfigs（CPU/内存单位：核 / GB）。
func (p *TencentCloudProvider) ListInstanceTypes(ctx context.Context, region string) ([]model.InstanceTypeSpec, error) {
	resp, err := p.callCVM(ctx, "DescribeInstanceTypeConfigs", region, url.Values{
		"Limit":  {strconv.Itoa(pageSize)},
		"Offset": {"0"},
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		InstanceTypeConfigSet []struct {
			InstanceType string `json:"InstanceType"`
			CPU          int    `json:"CPU"`
			Memory       int    `json:"Memory"` // GB
			Zone         string `json:"Zone"`
		} `json:"InstanceTypeConfigSet"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		return nil, fmt.Errorf("tencentcloud DescribeInstanceTypeConfigs 解析: %w", err)
	}
	seen := map[string]bool{}
	specs := make([]model.InstanceTypeSpec, 0)
	for _, t := range out.InstanceTypeConfigSet {
		if t.InstanceType == "" || seen[t.InstanceType] {
			continue
		}
		seen[t.InstanceType] = true
		specs = append(specs, model.InstanceTypeSpec{
			ID: t.InstanceType, CPU: t.CPU, MemoryMB: t.Memory * 1024,
			Provider: p.Name(), Region: region,
		})
	}
	return specs, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
