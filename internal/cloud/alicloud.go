package cloud

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
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

// AlicloudProvider 基于阿里云 ECS OpenAPI（RPC 风格，签名 v1）实现，
// 纯标准库，不引入 SDK。
//
// 签名算法文档：
//   - 签名机制（RPC Signature V1）:
//     https://help.aliyun.com/zh/sdk/product-overview/rpc-api-requests-and-signatures
//   - ECS API 参考: https://help.aliyun.com/zh/ecs/developer-reference/
type AlicloudProvider struct {
	accessKey string
	secretKey string
	// base API 端点，默认 https://ecs.aliyuncs.com（测试可替换）。
	base string
	// httpClient 默认 5s 超时。
	httpClient *http.Client
	// now 时间函数（签名与 nonce 用，测试可注入固定时间）。
	now func() time.Time
}

// NewAlicloudProvider 用注入的阿里云凭证构造 Provider。
func NewAlicloudProvider(ak, sk string) *AlicloudProvider {
	return &AlicloudProvider{
		accessKey:  ak,
		secretKey:  sk,
		base:       "https://ecs.aliyuncs.com",
		httpClient: &http.Client{Timeout: httpClientTimeout * time.Second},
		now:        time.Now,
	}
}

func (p *AlicloudProvider) Name() string { return "alicloud" }

const alicloudAPIVersion = "2014-05-26" // ECS API 版本

// ListImages 调用 DescribeImages（ImageOwnerAlias=system），
// https://help.aliyun.com/zh/ecs/developer-reference/api-describeimages
func (p *AlicloudProvider) ListImages(ctx context.Context, region string) ([]model.Image, error) {
	var resp struct {
		Images struct {
			Image []struct {
				ImageID     string `json:"ImageId"`
				ImageName   string `json:"ImageName"`
				OSName      string `json:"OSName"`
				Platform    string `json:"Platform"`
				Description string `json:"Description"`
			} `json:"Image"`
		} `json:"Images"`
	}
	params := url.Values{
		"RegionId":        {region},
		"ImageOwnerAlias": {"system"},
	}
	if err := p.callRPC(ctx, "DescribeImages", params, &resp); err != nil {
		return nil, err
	}
	images := make([]model.Image, 0, len(resp.Images.Image))
	for _, img := range resp.Images.Image {
		osType := img.OSName
		if osType == "" {
			osType = img.Platform
		}
		images = append(images, model.Image{
			ID: img.ImageID, Name: img.ImageName, Provider: p.Name(),
			Region: region, OSType: osType, Description: img.Description,
		})
	}
	return images, nil
}

// ListInstanceTypes 调用 DescribeInstanceTypes，
// https://help.aliyun.com/zh/ecs/developer-reference/api-describeinstancetypes
func (p *AlicloudProvider) ListInstanceTypes(ctx context.Context, region string) ([]model.InstanceTypeSpec, error) {
	var resp struct {
		InstanceTypes struct {
			InstanceType []struct {
				InstanceTypeID string  `json:"InstanceTypeId"`
				CPUCoreCount   int     `json:"CpuCoreCount"`
				MemorySize     float64 `json:"MemorySize"` // 单位 GB
			} `json:"InstanceType"`
		} `json:"InstanceTypes"`
	}
	if err := p.callRPC(ctx, "DescribeInstanceTypes", url.Values{"RegionId": {region}}, &resp); err != nil {
		return nil, err
	}
	specs := make([]model.InstanceTypeSpec, 0, len(resp.InstanceTypes.InstanceType))
	for _, it := range resp.InstanceTypes.InstanceType {
		specs = append(specs, model.InstanceTypeSpec{
			ID: it.InstanceTypeID, CPU: it.CPUCoreCount,
			MemoryMB: int(it.MemorySize * 1024), // GB -> MB
			Provider: p.Name(), Region: region,
		})
	}
	return specs, nil
}

// ListRegions 调用 DescribeRegions，
// https://help.aliyun.com/zh/ecs/developer-reference/api-describeregions
func (p *AlicloudProvider) ListRegions(ctx context.Context) ([]string, error) {
	var resp struct {
		Regions struct {
			Region []struct {
				RegionID string `json:"RegionId"`
			} `json:"Region"`
		} `json:"Regions"`
	}
	if err := p.callRPC(ctx, "DescribeRegions", url.Values{}, &resp); err != nil {
		return nil, err
	}
	regions := make([]string, 0, len(resp.Regions.Region))
	for _, r := range resp.Regions.Region {
		regions = append(regions, r.RegionID)
	}
	return regions, nil
}

// callRPC 发起一次 RPC 签名 v1 调用并解析 JSON 响应。
// 非 2xx 或响应携带 Code 字段时返回 *APIError（透传云端错误码）。
func (p *AlicloudProvider) callRPC(ctx context.Context, action string, extra url.Values, out any) error {
	query := url.Values{}
	for k, vs := range extra {
		for _, v := range vs {
			query.Add(k, v)
		}
	}
	ts := p.now().UTC().Format("2006-01-02T15:04:05Z")
	query.Set("Action", action)
	query.Set("Version", alicloudAPIVersion)
	query.Set("Format", "JSON")
	query.Set("AccessKeyId", p.accessKey)
	query.Set("SignatureMethod", "HMAC-SHA1")
	query.Set("SignatureVersion", "1.0")
	query.Set("SignatureNonce", fmt.Sprintf("ocw-%d", p.now().UnixNano()))
	query.Set("Timestamp", ts)
	query.Set("PageSize", strconv.Itoa(pageSize))
	query.Set("PageNumber", "1")
	query.Set("Signature", signAlicloudRPC(p.secretKey, http.MethodGet, query))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.base+"/?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("alicloud %s: %w", action, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("alicloud %s: 读取响应: %w", action, err)
	}
	var apiErr struct {
		Code    string `json:"Code"`
		Message string `json:"Message"`
	}
	_ = json.Unmarshal(body, &apiErr)
	if resp.StatusCode != http.StatusOK || apiErr.Code != "" {
		return &APIError{Provider: p.Name(), Code: apiErr.Code,
			Message: apiErr.Message, StatusCode: resp.StatusCode}
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("alicloud %s: 解析响应: %w", action, err)
	}
	return nil
}

// signAlicloudRPC 计算阿里云 RPC API 签名 v1（HMAC-SHA1）：
//
//	stringToSign = HTTPMethod + "&" + percentEncode("/") + "&" +
//	               percentEncode(canonicalizedQueryString)
//	signature    = base64(hmac_sha1(stringToSign, secretKey + "&"))
//
// 文档：https://help.aliyun.com/zh/sdk/product-overview/rpc-api-requests-and-signatures
func signAlicloudRPC(secretKey, httpMethod string, query url.Values) string {
	keys := make([]string, 0, len(query))
	for k := range query {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte('&')
		}
		sb.WriteString(percentEncodeRfc3986(k))
		sb.WriteByte('=')
		sb.WriteString(percentEncodeRfc3986(query.Get(k)))
	}
	stringToSign := httpMethod + "&" + percentEncodeRfc3986("/") + "&" +
		percentEncodeRfc3986(sb.String())
	mac := hmac.New(sha1.New, []byte(secretKey+"&"))
	mac.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// percentEncodeRfc3986 按 RFC 3986 做百分比编码（阿里云签名专用：
// 保留字除 A-Za-z0-9-_.~ 外全部编码，空格编码为 %20）。
func percentEncodeRfc3986(s string) string {
	// url.QueryEscape 空格编码为 "+"、"~" 编码为 %7E，均需修正。
	encoded := strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
	return strings.ReplaceAll(encoded, "%7E", "~")
}
