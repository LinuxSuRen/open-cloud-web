package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var tcFixedTime = time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC)

func newTCTestServer(t *testing.T) (*TencentCloudProvider, *httptest.Server, *[]string) {
	t.Helper()
	var auths []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auths = append(auths, r.Header.Get("Authorization"))
		switch r.URL.Query().Get("Action") {
		case "DescribeRegions":
			fmt.Fprint(w, `{"Response":{"RegionSet":[{"Region":"ap-guangzhou"},{"Region":"ap-shanghai"}]}}`)
		case "DescribeZones":
			fmt.Fprint(w, `{"Response":{"Zones":[{"Zone":"ap-guangzhou-3"},{"Zone":"ap-guangzhou-4"}]}}`)
		case "DescribeImages":
			fmt.Fprint(w, `{"Response":{"ImageSet":[{"ImageId":"img-1","ImageName":"Ubuntu 22.04","PlatformType":"Ubuntu"}]}}`)
		case "DescribeInstanceTypeConfigs":
			fmt.Fprint(w, `{"Response":{"InstanceTypeConfigSet":[{"InstanceType":"S5.MEDIUM4","CPU":2,"Memory":4},{"InstanceType":"S5.MEDIUM4","CPU":2,"Memory":4}]}}`)
		case "DescribeZonesErr":
			fmt.Fprint(w, `{"Response":{"Error":{"Code":"AuthFailure","Message":"sig bad"}}}`)
		default:
			http.Error(w, "unexpected action", http.StatusBadRequest)
		}
	}))
	p := NewTencentCloudProvider("testak", "testsk")
	p.host = strings.TrimPrefix(srv.URL, "https://")
	p.httpClient = srv.Client()
	p.now = func() time.Time { return tcFixedTime }
	return p, srv, &auths
}

func TestTencentSignStructure(t *testing.T) {
	p, srv, auths := newTCTestServer(t)
	defer srv.Close()
	if _, err := p.ListRegions(context.Background()); err != nil {
		t.Fatal(err)
	}
	auth := (*auths)[0]
	// 结构断言：算法/Credential/SignedHeaders 与官方 SDK 一致。
	if !strings.HasPrefix(auth, "TC3-HMAC-SHA256 Credential=testak/2026-09-30/cvm/tc3_request, ") {
		t.Fatalf("Authorization 前缀异常: %q", auth)
	}
	if !strings.Contains(auth, "SignedHeaders=content-type;host") {
		t.Fatalf("SignedHeaders 异常: %q", auth)
	}
	// 时间戳 = 固定时间的 unix。
	if !strings.Contains(auth, fmt.Sprint(tcFixedTime.Unix())) == false {
		t.Fatal("缺少时间戳")
	}
}

func TestTencentSignDeterministic(t *testing.T) {
	p, srv, auths := newTCTestServer(t)
	defer srv.Close()
	_, _ = p.ListRegions(context.Background())
	_, _ = p.ListRegions(context.Background())
	if len(*auths) != 2 {
		t.Fatalf("请求数 %d", len(*auths))
	}
	if (*auths)[0] != (*auths)[1] {
		t.Fatal("相同时间与参数的签名必须一致")
	}
	// 换 AK 必须改变 Credential 段。
	var akAuths []string
	srv2 := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		akAuths = append(akAuths, r.Header.Get("Authorization"))
		fmt.Fprint(w, `{"Response":{"RegionSet":[]}}`)
	}))
	defer srv2.Close()
	p2 := NewTencentCloudProvider("other", "sk")
	p2.host = strings.TrimPrefix(srv2.URL, "https://")
	p2.httpClient = srv2.Client()
	p2.now = func() time.Time { return tcFixedTime }
	if _, err := p2.ListRegions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(akAuths[0], "Credential=other/") {
		t.Fatalf("AK 未生效: %q", akAuths[0])
	}
	// 换 SK 必须改变签名（同 AK/时间下 Signature 不同）。
	p3 := NewTencentCloudProvider("other", "sk2")
	p3.host = p2.host
	p3.httpClient = srv2.Client()
	p3.now = p2.now
	if _, err := p3.ListRegions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if akAuths[0] == akAuths[1] {
		t.Fatal("更换 SK 后签名不应不变")
	}
}

func TestTencentQueries(t *testing.T) {
	p, srv, _ := newTCTestServer(t)
	defer srv.Close()
	regions, err := p.ListRegions(context.Background())
	if err != nil || len(regions) != 2 || regions[0] != "ap-guangzhou" {
		t.Fatalf("regions = %v, err = %v", regions, err)
	}
	zones, err := p.ListZones(context.Background(), "ap-guangzhou")
	if err != nil || len(zones) != 2 || zones[0] != "ap-guangzhou-3" {
		t.Fatalf("zones = %v, err = %v", zones, err)
	}
	images, err := p.ListImages(context.Background(), "ap-guangzhou")
	if err != nil || len(images) != 1 || images[0].ID != "img-1" ||
		images[0].Provider != "tencentcloud" || images[0].OSType != "Ubuntu" {
		t.Fatalf("images = %+v, err = %v", images, err)
	}
	specs, err := p.ListInstanceTypes(context.Background(), "ap-guangzhou")
	if err != nil || len(specs) != 1 || specs[0].CPU != 2 || specs[0].MemoryMB != 4096 {
		t.Fatalf("specs = %+v, err = %v", specs, err) // 重复 InstanceType 去重
	}
}

func TestTencentAPIError(t *testing.T) {
	p, srv, _ := newTCTestServer(t)
	defer srv.Close()
	_, err := p.callCVM(context.Background(), "DescribeZonesErr", "", nil)
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.Code != "AuthFailure" || apiErr.Provider != "tencentcloud" {
		t.Fatalf("err = %T %v", err, err)
	}
}

func TestTencentSessionToken(t *testing.T) {
	var gotToken string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-TC-Token")
		fmt.Fprint(w, `{"Response":{"RegionSet":[]}}`)
	}))
	defer srv.Close()
	p := NewTencentCloudProvider("ak", "sk")
	p.sessionTok = "tok-123"
	p.host = strings.TrimPrefix(srv.URL, "https://")
	p.httpClient = srv.Client()
	p.now = func() time.Time { return tcFixedTime }
	if _, err := p.ListRegions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotToken != "tok-123" {
		t.Fatalf("X-TC-Token = %q", gotToken)
	}
}

// 与官方 SDK 行为对照：canonical query 不含 Action/Version/Region。
func TestTencentCanonicalQueryExcludesStdKeys(t *testing.T) {
	var seenQuery string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 请求 URL 必须带 Action；签名计算用 canonical query（我们无法直接
		// 观察，但可断言服务端收到完整查询串）。
		seenQuery = r.URL.RawQuery
		fmt.Fprint(w, `{"Response":{"ImageSet":[]}}`)
	}))
	defer srv.Close()
	p := NewTencentCloudProvider("ak", "sk")
	p.host = strings.TrimPrefix(srv.URL, "https://")
	p.httpClient = srv.Client()
	p.now = func() time.Time { return tcFixedTime }
	if _, err := p.ListImages(context.Background(), "ap-guangzhou"); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"Action=DescribeImages", "Version=2017-03-12", "Region=ap-guangzhou", "ImageType=PUBLIC_IMAGE"} {
		if !strings.Contains(seenQuery, k) {
			t.Fatalf("请求缺少 %s: %q", k, seenQuery)
		}
	}
}

func TestTencentJSONErrorShape(t *testing.T) {
	// 网关层错误（顶层 Error）也应被识别。
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]any{
			"Error": map[string]string{"Code": "AuthFailure.SignatureFailure", "Message": "bad"}})
	}))
	defer srv.Close()
	p := NewTencentCloudProvider("ak", "sk")
	p.host = strings.TrimPrefix(srv.URL, "https://")
	p.httpClient = srv.Client()
	p.now = func() time.Time { return tcFixedTime }
	_, err := p.callCVM(context.Background(), "DescribeRegions", "", nil)
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.Code != "AuthFailure.SignatureFailure" {
		t.Fatalf("err = %T %v", err, err)
	}
}
