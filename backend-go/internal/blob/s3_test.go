package blob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 这组用例能证明什么、不能证明什么，先说清楚：
//
// 能证明的：
//   · PUT / GET / DELETE 的请求形状对（方法、路径、Content-Type、幂等删除）
//   · key 里的特殊字符（空格、中文、加号）被正确转义，且**签名与实际路径一致**
//   · 该签的东西都签进去了——改任何一个签名输入，签名必须变
//   · 不该影响签名的东西不影响它
//   · 错误信息里不泄漏 bucket / region / request id
//
// **不能**证明的：签出来的东西能被真实 S3 接受。
// 本沙箱没有 S3 凭据、也没有 MinIO，SigV4 与真实服务端的互操作性
// 只能等接上真环境才算验过。这一条已写进 PR 与部署文档，
// 不要因为这里全绿就以为对象存储那条路是通的。

func newFakeS3(t *testing.T, h http.HandlerFunc) (*S3, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &S3{
		Endpoint: srv.URL, Region: "ap-shanghai", Bucket: "tms-media",
		AccessKey: "AKIDEXAMPLE", SecretKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
		PathStyle: true, HTTP: srv.Client(),
	}, srv
}

// TestS3RoundTrip 一个最小的假 S3：存在内存 map 里，走完整条 PUT→GET→DELETE。
func TestS3RoundTrip(t *testing.T) {
	objs := map[string][]byte{}
	types := map[string]string{}
	s, _ := newFakeS3(t, func(w http.ResponseWriter, r *http.Request) {
		// path-style：/<bucket>/<key>
		p := strings.TrimPrefix(r.URL.Path, "/tms-media/")
		switch r.Method {
		case http.MethodPut:
			b, _ := io.ReadAll(r.Body)
			objs[p] = b
			types[p] = r.Header.Get("Content-Type")
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			b, ok := objs[p]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", types[p])
			w.Header().Set("ETag", `"abc123"`)
			_, _ = w.Write(b)
		case http.MethodDelete:
			delete(objs, p)
			w.WriteHeader(http.StatusNoContent)
		}
	})

	ctx := context.Background()
	if err := s.Put(ctx, "avatars/a.png", strings.NewReader("hello"), 5, "image/png"); err != nil {
		t.Fatalf("Put 失败：%v", err)
	}
	rc, info, err := s.Get(ctx, "avatars/a.png")
	if err != nil {
		t.Fatalf("Get 失败：%v", err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "hello" {
		t.Errorf("读回 %q，期望 hello", string(b))
	}
	if info.ContentType != "image/png" {
		t.Errorf("Content-Type=%q，期望 image/png", info.ContentType)
	}
	if info.ETag != "abc123" {
		t.Errorf("ETag=%q，期望 abc123（引号要剥掉）", info.ETag)
	}
	if err := s.Delete(ctx, "avatars/a.png"); err != nil {
		t.Fatalf("Delete 失败：%v", err)
	}
	if _, _, err := s.Get(ctx, "avatars/a.png"); !errors.Is(err, ErrNotFound) {
		t.Errorf("删后 Get 应回 ErrNotFound，实得 %v", err)
	}
}

// TestS3DeleteIsIdempotent 服务端回 404 时 Delete 也要成功。
func TestS3DeleteIsIdempotent(t *testing.T) {
	s, _ := newFakeS3(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	if err := s.Delete(context.Background(), "nope.png"); err != nil {
		t.Errorf("对不存在的对象 Delete 应成功，实得 %v", err)
	}
}

// TestS3KeyEscaping key 里的特殊字符要转义，且**签名里的路径和实际发出去的一致**。
//
// 这是最容易写出"只在某些文件名下才 403"的地方：
// 用 url.QueryEscape 的话空格会变成 "+"，签名按 "+" 算、服务端按 "%20" 解，
// 于是带空格的文件名全部签名不匹配，而不带空格的一切正常——
// 这种 bug 在测试数据里全是 a.png 时永远不会出现。
func TestS3KeyEscaping(t *testing.T) {
	var gotPath, gotAuth string
	s, _ := newFakeS3(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	})
	// 逐个钉死转义结果，而不是只说"别出现 +"。
	// SigV4 的规则很硬：只有 A-Za-z0-9 和 - . _ ~ 原样保留，其余一律 %XX 大写。
	for _, c := range []struct{ key, wantPath string }{
		// "+" 必须编成 %2B。Go 的 url.PathEscape 会把它原样留下（路径里它合法），
		// 但 S3 服务端规范化时会编码，两边 canonical request 就对不上——
		// 表现是文件名带加号的对象一律 403，其它一切正常。
		{"certs/a+b.png", "/tms-media/certs/a%2Bb.png"},
		// 空格必须是 %20，绝不能是 "+"（那是 QueryEscape 的行为）
		{"certs/a b.png", "/tms-media/certs/a%20b.png"},
		// "/" 是分隔符，不能被转义
		{"certs/a b/c d.png", "/tms-media/certs/a%20b/c%20d.png"},
		// 这四个符号是 unreserved，必须原样
		{"certs/a-b_c.d~e.png", "/tms-media/certs/a-b_c.d~e.png"},
	} {
		t.Run(c.key, func(t *testing.T) {
			if err := s.Put(context.Background(), c.key, strings.NewReader("x"), 1, "image/png"); err != nil {
				t.Fatalf("Put 失败：%v", err)
			}
			if gotPath != c.wantPath {
				t.Errorf("发出去的路径是 %s，期望 %s", gotPath, c.wantPath)
			}
			if !strings.HasPrefix(gotAuth, "AWS4-HMAC-SHA256 Credential=") {
				t.Errorf("Authorization 头格式不对：%s", gotAuth)
			}
		})
	}

	// 中文另测：UTF-8 每个字节都要单独 %XX
	t.Run("中文", func(t *testing.T) {
		if err := s.Put(context.Background(), "certs/身份证.png", strings.NewReader("x"), 1, ""); err != nil {
			t.Fatalf("Put 失败：%v", err)
		}
		if strings.ContainsAny(gotPath, "身份证") {
			t.Errorf("中文没被转义：%s", gotPath)
		}
		if !strings.HasSuffix(gotPath, ".png") || !strings.Contains(gotPath, "%") {
			t.Errorf("转义结果不对：%s", gotPath)
		}
	})
}

// sigOf 发一次请求，返回服务端收到的 Authorization 头
func sigOf(t *testing.T, mutate func(*S3), key, body, ct string) string {
	t.Helper()
	var auth string
	s, _ := newFakeS3(t, func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	})
	if mutate != nil {
		mutate(s)
	}
	if err := s.Put(context.Background(), key, strings.NewReader(body), int64(len(body)), ct); err != nil {
		t.Fatalf("Put 失败：%v", err)
	}
	return auth
}

// TestS3SignatureCoversInputs 改任何一个签名输入，签名都必须跟着变。
//
// 这条抓的是「漏签」——把某个字段忘在签名之外，是 SigV4 最典型的实现错误，
// 而且后果不是报错，是**同一个签名能被复用到不同的请求上**。
// 比如漏签 payload，攻击者截到一次上传就能改内容重放。
func TestS3SignatureCoversInputs(t *testing.T) {
	base := sigOf(t, nil, "a/x.png", "hello", "image/png")

	for _, c := range []struct {
		name   string
		sig    string
		reason string
	}{
		{"改 key", sigOf(t, nil, "a/y.png", "hello", "image/png"), "路径没进签名 → 同一签名可用于任意对象"},
		{"改内容", sigOf(t, nil, "a/x.png", "HELLO", "image/png"), "payload 没进签名 → 截获后可改内容重放"},
		{"改 Content-Type", sigOf(t, nil, "a/x.png", "hello", "text/html"),
			"Content-Type 没进签名 → 可把图片重放成 text/html，直接变存储型 XSS"},
		{"改 SecretKey", sigOf(t, func(s *S3) { s.SecretKey = "另一把钥匙" }, "a/x.png", "hello", "image/png"),
			"密钥没参与推导"},
		{"改 Region", sigOf(t, func(s *S3) { s.Region = "us-east-1" }, "a/x.png", "hello", "image/png"),
			"region 没进 scope → 跨区重放"},
		{"改 Bucket", sigOf(t, func(s *S3) { s.Bucket = "别的桶" }, "a/x.png", "hello", "image/png"),
			"path-style 下 bucket 在路径里，必须进签名"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.sig == base {
				t.Errorf("%s 之后签名没变——%s", c.name, c.reason)
			}
		})
	}
}

// TestS3PayloadHashHeader x-amz-content-sha256 必须是 payload 的真实 SHA256。
//
// 这个值可以独立核对：GET 没有 body，它就必须是空串的 SHA256，
// 也就是那个众所周知的常量 e3b0c442...b855。
// 拿一个不依赖本实现的已知常量来校，才算真的验过，
// 否则就是「用同一段代码验同一段代码」。
func TestS3PayloadHashHeader(t *testing.T) {
	const emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	var gotGet, gotPut string
	s, _ := newFakeS3(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			gotGet = r.Header.Get("x-amz-content-sha256")
		} else {
			gotPut = r.Header.Get("x-amz-content-sha256")
		}
		w.WriteHeader(http.StatusOK)
	})
	_, _, _ = s.Get(context.Background(), "a/x.png")
	if gotGet != emptySHA256 {
		t.Errorf("GET 的 x-amz-content-sha256 = %q，空 body 应为 %s", gotGet, emptySHA256)
	}

	body := "hello world"
	sum := sha256.Sum256([]byte(body))
	want := hex.EncodeToString(sum[:])
	_ = s.Put(context.Background(), "a/x.png", strings.NewReader(body), int64(len(body)), "")
	if gotPut != want {
		t.Errorf("PUT 的 x-amz-content-sha256 = %q，期望 %s", gotPut, want)
	}
}

// TestS3ErrorLeaksNothing 错误信息里不能带服务端响应体。
//
// S3 的错误 XML 里有 bucket 名、region、request id。这个 error 一路会冒到
// HTTP 响应和日志里去——那是把内网结构送到外面。
func TestS3ErrorLeaksNothing(t *testing.T) {
	s, _ := newFakeS3(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<Error><Code>SignatureDoesNotMatch</Code>` +
			`<BucketName>tms-media-prod</BucketName><RequestId>ABC123XYZ</RequestId></Error>`))
	})
	err := s.Put(context.Background(), "a/x.png", strings.NewReader("x"), 1, "")
	if err == nil {
		t.Fatal("403 应当报错")
	}
	for _, leak := range []string{"tms-media-prod", "ABC123XYZ", "SignatureDoesNotMatch"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("错误信息里泄漏了 %q：%v", leak, err)
		}
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("错误信息里应当留下状态码，便于排查：%v", err)
	}
}

// TestS3VirtualHostStyle 非 path-style 时 bucket 进主机名。
func TestS3VirtualHostStyle(t *testing.T) {
	s := &S3{
		Endpoint: "https://cos.ap-shanghai.myqcloud.com", Bucket: "tms-media",
		Region: "ap-shanghai", AccessKey: "k", SecretKey: "s",
	}
	full, _ := s.objURL("avatars/a.png")
	if !strings.HasPrefix(full, "https://tms-media.cos.ap-shanghai.myqcloud.com/avatars/a.png") {
		t.Errorf("virtual-host 风格地址不对：%s", full)
	}
	s.PathStyle = true
	full, _ = s.objURL("avatars/a.png")
	if full != "https://cos.ap-shanghai.myqcloud.com/tms-media/avatars/a.png" {
		t.Errorf("path 风格地址不对：%s", full)
	}
}
