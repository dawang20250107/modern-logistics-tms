// Package blob 媒体文件的存放位置。
//
// 存在的理由是一条部署约束：媒体文件（头像、司机证件、回单、合同）此前
// 直接写在网关容器的本地磁盘上。单副本没问题，一旦起两个副本就坏了——
// 上传落在 A 的盘上，后面那次 GET 被路由到 B，B 上没有这个文件，404。
// 而且它是**间歇性**的：同一张图刷新几次可能好几次坏几次，取决于路由到哪个副本。
// 这类故障在预发（通常单副本）永远复现不出来。
//
// 两种实现：
//   - Local：写本地目录。默认，单副本部署与本地开发用它，行为与原先完全一致。
//   - S3：任何 S3 兼容对象存储（AWS S3、腾讯云 COS、MinIO、阿里云 OSS 的 S3 网关）。
//
// 读也走网关（而不是给浏览器发 302 到预签名 URL）：媒体里有身份证、行驶证、
// 签收回单，这些是可以直接读到人脸和签名的东西。让它们只经过一个出口，
// 将来要加鉴权、加水印、加访问审计，都只有一个地方要改。
// 代价是网关在数据通路上——媒体流量大到成为瓶颈时再换 302，那是另一个问题。
package blob

import (
	"context"
	"errors"
	"io"
)

// ErrNotFound 对象不存在。调用方据此回 404，不要把底层错误原样透出去——
// S3 的错误体里带 bucket 名和 request id，那是内网结构。
var ErrNotFound = errors.New("blob: 对象不存在")

// Store 媒体存放。key 一律是**正斜杠相对路径**（如 "avatars/xxx.png"），
// 与库里存的值、与 /media/ 后面那一段完全一致——三处用同一个字符串，
// 就不会出现"库里存的和实际路径对不上"这种只能靠人肉核对的问题。
type Store interface {
	// Put 写入。size 为 -1 表示未知长度。
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	// Get 读取。不存在时返回 ErrNotFound。
	Get(ctx context.Context, key string) (io.ReadCloser, *ObjectInfo, error)
	// Delete 删除。对象本就不存在时返回 nil——删除要幂等，
	// 否则"删掉旧头像"这种清理动作会因为文件早已不在而把整个请求搞失败。
	Delete(ctx context.Context, key string) error
	// Kind 返回实现名，用于启动日志与 /readyz。
	Kind() string
}

type ObjectInfo struct {
	Size        int64
	ContentType string
	ETag        string
}
