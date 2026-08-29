package blob

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// S3 任何 S3 兼容对象存储：AWS S3、腾讯云 COS、MinIO、阿里云 OSS 的 S3 网关。
//
// 自己签名而不引 aws-sdk-go-v2：整个 SDK 为了这三个动作（PUT / GET / DELETE）
// 要拖进来几十个包，而 SigV4 本身就是一段确定性的哈希拼接。
// 依赖越少，升级时要跟的东西越少——这套系统整体就是这个取向（没有 ORM、
// 没有 Redis、Prometheus 导出也是手写的）。
//
// 代价说清楚：这里只实现了签名算法本身，没有 SDK 的重试、分片上传、
// 自适应超时。媒体是几百 KB 的图片，用不上分片；重试交给调用方。
type S3 struct {
	Endpoint  string // 如 https://cos.ap-shanghai.myqcloud.com（不含 bucket）
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
	// PathStyle 用 <endpoint>/<bucket>/<key> 而不是 <bucket>.<endpoint>/<key>。
	// MinIO 和大多数自建网关只支持前者；AWS 两种都行。
	PathStyle bool
	HTTP      *http.Client
}

func (s *S3) Kind() string { return "s3:" + s.Bucket + "@" + s.Endpoint }

func (s *S3) client() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second}
}

// awsURIEncode 按 SigV4 的规则给一个路径段转义。
//
// 规则：只有 A-Za-z0-9 和 - . _ ~ 这四个符号保持原样，其余一律 %XX（大写十六进制）。
//
// 不能用 url.PathEscape：它按 RFC 3986 判"路径里合法就不转义"，
// 于是 "+" 被原样留下。但 S3 服务端做规范化时会把它编成 %2B，
// 两边的 canonical request 就对不上——表现是**文件名带加号的对象一律 403**，
// 而其它文件一切正常。这类 bug 在测试数据全是 a.png 时永远不会出现。
// （用 url.QueryEscape 更糟：它把空格编成 "+"。）
func awsURIEncode(seg string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(seg); i++ {
		c := seg[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '.', c == '_', c == '~':
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0x0f])
		}
	}
	return b.String()
}

// objURL 拼对象地址。
//
// key 里的 "/" 保留成路径分隔符，每一段单独按 SigV4 规则转义。
func (s *S3) objURL(key string) (string, string) {
	segs := strings.Split(strings.TrimPrefix(key, "/"), "/")
	for i, seg := range segs {
		segs[i] = awsURIEncode(seg)
	}
	escaped := strings.Join(segs, "/")
	base := strings.TrimSuffix(s.Endpoint, "/")
	if s.PathStyle {
		return base + "/" + s.Bucket + "/" + escaped, "/" + s.Bucket + "/" + escaped
	}
	// virtual-host 风格：bucket 进主机名
	u, err := url.Parse(base)
	if err != nil {
		return base + "/" + escaped, "/" + escaped
	}
	u.Host = s.Bucket + "." + u.Host
	return u.String() + "/" + escaped, "/" + escaped
}

func hmacSHA256(key []byte, s string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(s))
	return h.Sum(nil)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// sign 按 AWS Signature Version 4 给请求签名。
//
// 几处容易写错、写错了又只在特定输入下才暴露的：
//   - payload 必须先算 SHA256 并放进 x-amz-content-sha256 头，这个头本身也参与签名
//   - 规范请求里 header 名要小写、按名排序，值要 trim 且压缩内部连续空格
//   - 只有列进 SignedHeaders 的头才参与签名，顺序必须与规范请求一致
//   - host 头必须签（否则同一个签名能被重放到别的 endpoint）
func (s *S3) sign(req *http.Request, payload []byte) {
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")
	payloadHash := sha256Hex(payload)

	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	if req.Host == "" {
		req.Host = req.URL.Host
	}

	// 参与签名的头：host + 所有 x-amz-*，外加 content-type（若有）
	signed := []string{"host"}
	for k := range req.Header {
		lk := strings.ToLower(k)
		if strings.HasPrefix(lk, "x-amz-") || lk == "content-type" {
			signed = append(signed, lk)
		}
	}
	sort.Strings(signed)

	var canonHeaders strings.Builder
	for _, h := range signed {
		v := ""
		if h == "host" {
			v = req.Host
		} else {
			v = strings.Join(req.Header.Values(h), ",")
		}
		canonHeaders.WriteString(h + ":" + strings.Join(strings.Fields(v), " ") + "\n")
	}
	signedHeaders := strings.Join(signed, ";")

	canonicalQuery := req.URL.Query().Encode() // 已按键排序
	canonicalReq := strings.Join([]string{
		req.Method,
		req.URL.EscapedPath(),
		canonicalQuery,
		canonHeaders.String(),
		signedHeaders,
		payloadHash,
	}, "\n")

	scope := dateStamp + "/" + s.Region + "/s3/aws4_request"
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256", amzDate, scope, sha256Hex([]byte(canonicalReq)),
	}, "\n")

	k := hmacSHA256([]byte("AWS4"+s.SecretKey), dateStamp)
	k = hmacSHA256(k, s.Region)
	k = hmacSHA256(k, "s3")
	k = hmacSHA256(k, "aws4_request")
	sig := hex.EncodeToString(hmacSHA256(k, stringToSign))

	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		s.AccessKey, scope, signedHeaders, sig))
}

func (s *S3) do(ctx context.Context, method, key string, payload []byte, contentType string) (*http.Response, error) {
	full, _ := s.objURL(key)
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, full, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if payload != nil {
		req.ContentLength = int64(len(payload))
	}
	s.sign(req, payload)
	return s.client().Do(req)
}

func (s *S3) Put(ctx context.Context, key string, r io.Reader, _ int64, contentType string) error {
	// 全读进内存再签。SigV4 要先知道 payload 的 SHA256 才能签，
	// 流式要走 STREAMING-AWS4-HMAC-SHA256 分块协议——那套复杂得多，
	// 而这里的对象是几百 KB 的图片，上限由调用方（头像 2MB）卡着。
	// 将来要传大文件，这里必须改，别让它默默吃内存。
	payload, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	resp, err := s.do(ctx, http.MethodPut, key, payload, contentType)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return s3err("PUT", key, resp)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, *ObjectInfo, error) {
	resp, err := s.do(ctx, http.MethodGet, key, nil, "")
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		_ = resp.Body.Close()
		return nil, nil, ErrNotFound
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		return nil, nil, s3err("GET", key, resp)
	}
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/octet-stream"
	}
	return resp.Body, &ObjectInfo{
		Size:        resp.ContentLength,
		ContentType: ct,
		ETag:        strings.Trim(resp.Header.Get("ETag"), `"`),
	}, nil
}

func (s *S3) Delete(ctx context.Context, key string) error {
	resp, err := s.do(ctx, http.MethodDelete, key, nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// S3 删不存在的对象回 204，这里把 404 也当成功——删除要幂等
	if resp.StatusCode/100 != 2 && resp.StatusCode != http.StatusNotFound {
		return s3err("DELETE", key, resp)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// s3err 造错误时**不带**响应体。
// S3 的错误 XML 里有 bucket 名、region、request id，那是内网结构；
// 这个 error 一路会冒到 HTTP 响应和日志里去。
func s3err(op, key string, resp *http.Response) error {
	return fmt.Errorf("blob: %s %s 失败：HTTP %d", op, key, resp.StatusCode)
}
