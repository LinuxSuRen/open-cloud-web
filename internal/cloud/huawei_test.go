package cloud

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newHwTestServer(t *testing.T) (*HuaweiCloudProvider, *httptest.Server) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v3/projects":
			fmt.Fprint(w, `{"projects":[{"id":"pid-0f4a","name":"cn-north-4"}]}`)
		case r.URL.Path == "/v3/regions":
			fmt.Fprint(w, `{"regions":[{"id":"cn-north-4"},{"id":"cn-east-3"}]}`)
		case r.URL.Path == "/v2.1/pid-0f4a/os-availability-zone":
			fmt.Fprint(w, `{"availabilityZoneInfo":[{"zoneName":"cn-north-4a","zoneState":{"available":true}},{"zoneName":"cn-north-4b","zoneState":{"available":false}}]}`)
		case r.URL.Path == "/v1/pid-0f4a/cloudservers/flavors":
			fmt.Fprint(w, `{"flavors":[{"id":"c7.large.2","vcpus":"2","ram":4096},{"id":"s7.large.2","vcpus":"2","ram":4096}]}`)
		case strings.HasPrefix(r.URL.Path, "/v2/cloudimages"):
			fmt.Fprint(w, `{"images":[{"id":"img-abc","name":"Ubuntu 22.04 64bit","__os_type":"Linux","__os_version":"Ubuntu 22.04 64bit"}]}`)
		default:
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusBadRequest)
		}
	}))
	p := NewHuaweiCloudProvider("testak", "testsk")
	p.iamHost = strings.TrimPrefix(srv.URL, "https://")
	p.httpClient = srv.Client()
	p.now = func() time.Time { return tcFixedTime }
	return p, srv
}

// hwRedirect 把区域级服务端点（ECS/IMS）重定向到测试服务器。
func hwRedirect(p *HuaweiCloudProvider, srv *httptest.Server) {
	base := strings.TrimPrefix(srv.URL, "https://")
	p.endpointFor = func(service, region string) string { return base }
}

func TestHuaweiProjectResolution(t *testing.T) {
	p, srv := newHwTestServer(t)
	defer srv.Close()
	hwRedirect(p, srv)
	zones, err := p.ListZones(context.Background(), "cn-north-4")
	if err != nil {
		t.Fatalf("ListZones: %v（IAM 项目解析应成功）", err)
	}
	if len(zones) != 1 || zones[0] != "cn-north-4a" {
		t.Fatalf("zones = %v（不可用 AZ 应被过滤）", zones)
	}
	// 项目缓存：再次调用不再请求 /v3/projects。
	p.projects["cn-north-4"] = "pid-0f4a"
	if _, err := p.ListInstanceTypes(context.Background(), "cn-north-4"); err != nil {
		t.Fatalf("ListInstanceTypes: %v", err)
	}
}

func TestHuaweiQueries(t *testing.T) {
	p, srv := newHwTestServer(t)
	defer srv.Close()
	hwRedirect(p, srv)
	regions, err := p.ListRegions(context.Background())
	if err != nil || len(regions) != 2 || regions[0] != "cn-north-4" {
		t.Fatalf("regions = %v, err = %v", regions, err)
	}
	images, err := p.ListImages(context.Background(), "cn-north-4")
	if err != nil || len(images) != 1 || images[0].ID != "img-abc" ||
		images[0].Provider != "huaweicloud" || images[0].OSType != "Linux" {
		t.Fatalf("images = %+v, err = %v", images, err)
	}
	specs, err := p.ListInstanceTypes(context.Background(), "cn-north-4")
	if err != nil || len(specs) != 2 || specs[0].CPU != 2 || specs[0].MemoryMB != 4096 {
		t.Fatalf("specs = %+v, err = %v", specs, err)
	}
}

func TestHuaweiSignStructure(t *testing.T) {
	var auth, xDate string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		xDate = r.Header.Get("X-Sdk-Date")
		fmt.Fprint(w, `{"regions":[]}`)
	}))
	defer srv.Close()
	p := NewHuaweiCloudProvider("hwak", "hwsk")
	p.iamHost = strings.TrimPrefix(srv.URL, "https://")
	p.httpClient = srv.Client()
	p.now = func() time.Time { return tcFixedTime }
	if _, err := p.ListRegions(context.Background()); err != nil {
		t.Fatal(err)
	}
	// 与官方 SDK 格式对照：Access=AK（非 Credential）。
	if !strings.HasPrefix(auth, "SDK-HMAC-SHA256 Access=hwak, SignedHeaders=content-type;host;x-sdk-date, Signature=") {
		t.Fatalf("Authorization 格式异常: %q", auth)
	}
	if xDate != "20260930T080000Z" {
		t.Fatalf("X-Sdk-Date = %q", xDate)
	}
}

func TestHuaweiErrorShapes(t *testing.T) {
	// 云服务错误 {"error":{"code","message"}}。
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"code":"APIGW.0301","message":"Incorrect IAM authentication"}}`)
	}))
	defer srv.Close()
	p := NewHuaweiCloudProvider("ak", "sk")
	p.iamHost = strings.TrimPrefix(srv.URL, "https://")
	p.httpClient = srv.Client()
	p.now = func() time.Time { return tcFixedTime }
	_, err := p.ListRegions(context.Background())
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.Code != "APIGW.0301" || apiErr.Provider != "huaweicloud" {
		t.Fatalf("err = %T %v", err, err)
	}
}

func TestHuaweiProjectNotFound(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"projects":[]}`)
	}))
	defer srv.Close()
	p := NewHuaweiCloudProvider("ak", "sk")
	p.iamHost = strings.TrimPrefix(srv.URL, "https://")
	p.httpClient = srv.Client()
	p.now = func() time.Time { return tcFixedTime }
	_, err := p.ListZones(context.Background(), "cn-mars-1")
	if err == nil || !strings.Contains(err.Error(), "cn-mars-1") {
		t.Fatalf("未知区域应报错: %v", err)
	}
}
