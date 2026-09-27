package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// fixedTime 固定时间（与手工推导的已知签名向量配套）。
var fixedTime = time.Date(2026, 7, 14, 2, 11, 0, 0, time.UTC)

func TestSignAlicloudRPC_KnownVector(t *testing.T) {
	tests := []struct {
		name   string
		secret string
		query  url.Values
		want   string
	}{
		{
			name:   "官方示例风格请求",
			secret: "testsecret",
			query: url.Values{
				"Action":           {"DescribeRegions"},
				"Format":           {"JSON"},
				"AccessKeyId":      {"testid"},
				"SignatureMethod":  {"HMAC-SHA1"},
				"SignatureVersion": {"1.0"},
				"SignatureNonce":   {"9e7f804f-1e9a-4b7c-8dd8-45d18b0a2446"},
				"Timestamp":        {"2026-07-14T02:11:00Z"},
				"Version":          {"2014-05-26"},
			},
			// 已知向量（独立按文档算法推导）：
			want: "ELgA/E+vGwnOv7QmMtsXC7D/Naw=",
		},
		{
			name:   "含空格与保留字",
			secret: "another secret",
			query: url.Values{
				"Action":  {"Describe Images"},
				"Special": {"a~b:c*"},
			},
			// 已知向量（独立按文档算法推导）：
			want: "kC8JK87RLv3FudR4FFfTpHRIOFo=",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := signAlicloudRPC(tt.secret, http.MethodGet, tt.query)
			if got != tt.want {
				t.Fatalf("签名 = %q, want %q", got, tt.want)
			}
			// 确定性：同样输入必须得到同样签名。
			if again := signAlicloudRPC(tt.secret, http.MethodGet, tt.query); again != got {
				t.Fatalf("签名不确定: %q vs %q", got, again)
			}
		})
	}
}

func TestVolcengineSign_KnownVector(t *testing.T) {
	const amzDate = "20260714T021100Z"
	query := url.Values{
		"PageSize":   {"100"},
		"PageNumber": {"1"},
		"Visibility": {"public"},
	}
	got := volcengineSign("testsecret", "testak", "cn-north-1", "compute",
		http.MethodGet, "open.volcengine.com", "ListImages", "2020-04-01",
		amzDate, query)
	want := "HMAC-SHA256 Credential=testak/20260714/cn-north-1/compute/request, " +
		"SignedHeaders=content-type;host;x-action;x-date;x-version, " +
		"Signature=d02acf57dba0bbd6e09a18ca8116032269881234a2b0298b108dbd38906fafc2"
	if got != want {
		t.Fatalf("Authorization =\n%s\nwant\n%s", got, want)
	}
	if again := volcengineSign("testsecret", "testak", "cn-north-1", "compute",
		http.MethodGet, "open.volcengine.com", "ListImages", "2020-04-01",
		amzDate, query); again != got {
		t.Fatal("签名不确定")
	}
	// 时间或密钥变化必须改变签名。
	if same := volcengineSign("other", "testak", "cn-north-1", "compute",
		http.MethodGet, "open.volcengine.com", "ListImages", "2020-04-01",
		amzDate, query); same == got {
		t.Fatal("更换密钥后签名不应不变")
	}
}

func TestPercentEncodeRfc3986(t *testing.T) {
	tests := []struct{ in, want string }{
		{"abcXYZ019-_.~", "abcXYZ019-_.~"}, // 非保留字不编码
		{"a b", "a%20b"},                   // 空格 -> %20（不是 +）
		{"~", "~"},                         // ~ 属非保留字
		{":", "%3A"},
		{"*", "%2A"},
		{"中文", "%E4%B8%AD%E6%96%87"},
		{"/", "%2F"},
	}
	for _, tt := range tests {
		if got := percentEncodeRfc3986(tt.in); got != tt.want {
			t.Errorf("percentEncodeRfc3986(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestRegistry(t *testing.T) {
	resetRegistry()
	defer resetRegistry()
	if _, err := Get("alicloud"); err == nil {
		t.Fatal("未注册时 Get 应报错")
	}
	Register(NewAlicloudProvider("ak", "sk"))
	Register(NewVolcengineProvider("ak", "sk"))
	if names := Names(); len(names) != 2 || names[0] != "alicloud" || names[1] != "volcengine" {
		t.Fatalf("Names = %v", names)
	}
	if p, err := Get("volcengine"); err != nil || p.Name() != "volcengine" {
		t.Fatalf("Get(volcengine) = %v, %v", p, err)
	}
}

func TestAlicloudProviderListRegions(t *testing.T) {
	var gotPath, gotSig, gotAction string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotSig = r.URL.Query().Get("Signature")
		gotAction = r.URL.Query().Get("Action")
		fmt.Fprint(w, `{"Regions":{"Region":[{"RegionId":"cn-hangzhou"},{"RegionId":"cn-beijing"}]}}`)
	}))
	defer srv.Close()

	p := NewAlicloudProvider("testid", "testsecret")
	p.base = srv.URL
	p.now = func() time.Time { return fixedTime }
	regions, err := p.ListRegions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(regions) != 2 || regions[0] != "cn-hangzhou" || regions[1] != "cn-beijing" {
		t.Fatalf("regions = %v", regions)
	}
	if gotPath != "/" || gotAction != "DescribeRegions" {
		t.Fatalf("请求路径/Action 异常: %q %q", gotPath, gotAction)
	}
	// 固定时间下（nonce 确定）签名应确定。
	first := gotSig
	_, _ = p.ListRegions(context.Background())
	if gotSig != first {
		t.Fatalf("签名不确定: %q vs %q", first, gotSig)
	}
}

func TestAlicloudProviderListImagesAndTypes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("Action") {
		case "DescribeImages":
			fmt.Fprint(w, `{"Images":{"Image":[{"ImageId":"m-1","ImageName":"Ubuntu 22.04","OSName":"ubuntu 22.04 64bit","Description":"公共镜像"}]}}`)
		case "DescribeInstanceTypes":
			fmt.Fprint(w, `{"InstanceTypes":{"InstanceType":[{"InstanceTypeId":"ecs.e-c1m1.large","CpuCoreCount":2,"MemorySize":4}]}}`)
		default:
			http.Error(w, "unexpected", http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	p := NewAlicloudProvider("ak", "sk")
	p.base = srv.URL
	images, err := p.ListImages(context.Background(), "cn-hangzhou")
	if err != nil || len(images) != 1 ||
		images[0].ID != "m-1" || images[0].Provider != "alicloud" ||
		images[0].Region != "cn-hangzhou" || images[0].OSType != "ubuntu 22.04 64bit" {
		t.Fatalf("images = %+v, err = %v", images, err)
	}
	specs, err := p.ListInstanceTypes(context.Background(), "cn-hangzhou")
	if err != nil || len(specs) != 1 || specs[0].CPU != 2 || specs[0].MemoryMB != 4096 {
		t.Fatalf("specs = %+v, err = %v", specs, err)
	}
}

func TestAlicloudProviderAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"Code":"InvalidAccessKeyId.NotFound","Message":"The Access Key ID does not exist."}`)
	}))
	defer srv.Close()

	p := NewAlicloudProvider("bad", "bad")
	p.base = srv.URL
	_, err := p.ListRegions(context.Background())
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("应返回 *APIError, got %T: %v", err, err)
	}
	if apiErr.Code != "InvalidAccessKeyId.NotFound" || apiErr.StatusCode != http.StatusForbidden {
		t.Fatalf("APIError = %+v", apiErr)
	}
	if !strings.Contains(apiErr.Error(), "InvalidAccessKeyId.NotFound") {
		t.Fatalf("错误信息应透传错误码: %v", apiErr)
	}
}

func TestVolcengineProviderQueries(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ps := r.URL.Query().Get("PageSize"); ps != "" && ps != "100" {
			http.Error(w, "unexpected query", http.StatusBadRequest)
			return
		}
		switch r.Header.Get("X-Action") {
		case "ListImages":
			fmt.Fprint(w, `{"ResponseMetadata":{},"Result":{"Images":[{"ImageID":"image-1","Name":"Ubuntu","Description":"desc","OSName":"Ubuntu 22.04"}]}}`)
		case "ListInstanceTypes":
			fmt.Fprint(w, `{"InstanceTypes":[{"InstanceTypeId":"ecs.g1.large","CPU":{"CoreCount":2},"Memory":{"Size":4}}]}`)
		case "ListZones":
			fmt.Fprint(w, `{"Result":{"Zones":[{"ZoneId":"cn-beijing-a"},{"ZoneId":"cn-beijing-b"}]}}`)
		default:
			http.Error(w, "unexpected action", http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	p := NewVolcengineProvider("testak", "testsecret")
	p.host = strings.TrimPrefix(srv.URL, "https://")
	p.httpClient = srv.Client()
	p.now = func() time.Time { return fixedTime }

	images, err := p.ListImages(context.Background(), "cn-beijing")
	if err != nil || len(images) != 1 || images[0].ID != "image-1" ||
		images[0].Provider != "volcengine" || images[0].OSType != "Ubuntu 22.04" {
		t.Fatalf("images = %+v, err = %v", images, err)
	}
	specs, err := p.ListInstanceTypes(context.Background(), "cn-beijing")
	if err != nil || len(specs) != 1 || specs[0].CPU != 2 || specs[0].MemoryMB != 4096 {
		t.Fatalf("specs = %+v, err = %v", specs, err)
	}
	zones, err := p.ListRegions(context.Background())
	if err != nil || len(zones) != 2 || zones[0] != "cn-beijing-a" {
		t.Fatalf("zones = %v, err = %v", zones, err)
	}
}

func TestVolcengineProviderSignedHeaderAndError(t *testing.T) {
	var auth string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"ResponseMetadata":{"Error":{"Code":"InvalidAccessKey","CodeN":401,"Message":"invalid access key"}}}`)
	}))
	defer srv.Close()

	p := NewVolcengineProvider("testak", "testsecret")
	p.host = strings.TrimPrefix(srv.URL, "https://")
	p.httpClient = srv.Client()
	p.now = func() time.Time { return fixedTime }

	_, err := p.ListRegions(context.Background())
	if auth == "" || !strings.HasPrefix(auth, "HMAC-SHA256 Credential=testak/") ||
		!strings.Contains(auth, "SignedHeaders=content-type;host;x-action;x-date;x-version") {
		t.Fatalf("Authorization 头异常: %q", auth)
	}
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.Code != "InvalidAccessKey" || apiErr.Message != "invalid access key" {
		t.Fatalf("err = %T %v", err, err)
	}
}

func TestFirstListAndHelpers(t *testing.T) {
	body := []byte(`{"Result":{"Items":[{"A":1},{"A":2}]}}`)
	if got := firstList(body, []string{"Result", "Items"}, []string{"Items"}); len(got) != 2 {
		t.Fatalf("firstList = %v", got)
	}
	var m map[string]any
	_ = json.Unmarshal([]byte(`{"CPU":{"CoreCount":8},"Name":"x"}`), &m)
	if num(m, "CPU.CoreCount") != 8 || str(m, "Name") != "x" || str(m, "Missing") != "" {
		t.Fatal("str/num 候选取值异常")
	}
}
