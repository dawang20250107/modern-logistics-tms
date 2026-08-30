package blob

import (
	"context"
	"errors"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
)

// Local 写本地目录。默认实现，行为与接对象存储之前完全一致。
type Local struct{ Root string }

func NewLocal(root string) *Local { return &Local{Root: root} }

func (l *Local) Kind() string { return "local:" + l.Root }

// abs 把 key 翻成磁盘路径。两道检查，各管各的一件事——这一点值得写清楚，
// 免得后面有人以为其中一道是冗余的就删掉：
//
//  1. 段级拒绝（""、"."、".."、反斜杠）——管的是**别名**。
//     没有它的话 "./x" 和 "x"、"a//b" 和 "a/b" 会指向同一个对象，
//     而 /media/./x 会正常返回 /media/x 的内容而不是 404。
//     两个不同的 key 映射到一个文件是个安静的坑。
//  2. 前缀兜底（fullAbs 必须在 root 之下）——管的是**越权**，
//     这才是真正的安全边界。filepath.Join 会把 ".." 解析掉，
//     "../secret" 算出来落在 root 外面，靠这一条挡住。
//
// 把段级检查去掉做变异验证时，".." 那几个用例仍然是绿的（前缀兜底接住了），
// 只有 "./x" 变红——正好说明这两道各自在管不同的事。
//
// key 有一部分来自请求路径（/media/<key>）。原先这条路由靠 http.FileServer
// 自带的清理挡着，抽象成 Store 之后那层保障就没了：换实现时最容易丢的
// 恰恰是这种"本来由框架顺手提供"的保护，它不会编译报错、正常用例也照过。
func (l *Local) abs(key string) (string, error) {
	k := strings.TrimPrefix(key, "/")
	if k == "" {
		return "", ErrNotFound
	}
	for _, seg := range strings.Split(k, "/") {
		// 空段（"a//b"）、"." 、".." 一律拒。反斜杠也拒：Windows 上它是分隔符，
		// 而这里的 key 约定只用正斜杠。
		if seg == "" || seg == "." || seg == ".." || strings.ContainsAny(seg, `\`) {
			return "", ErrNotFound
		}
	}
	full := filepath.Join(l.Root, filepath.FromSlash(k))
	root, err := filepath.Abs(l.Root)
	if err != nil {
		return "", err
	}
	fullAbs, err := filepath.Abs(full)
	if err != nil {
		return "", err
	}
	// 兜底再比一次前缀。带分隔符比较，否则 /data/media-evil 会被判成
	// /data/media 的子路径。
	if fullAbs != root && !strings.HasPrefix(fullAbs, root+string(filepath.Separator)) {
		return "", ErrNotFound
	}
	return fullAbs, nil
}

func (l *Local) Put(_ context.Context, key string, r io.Reader, _ int64, _ string) error {
	abs, err := l.abs(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	// 先写临时文件再改名：写到一半进程被杀时，留下的是一个没人引用的 .tmp，
	// 而不是一个大小对不上的"半张图"——后者会被当成正常文件读出来。
	tmp, err := os.CreateTemp(filepath.Dir(abs), ".upload-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName) // rename 成功后这里删的是个不存在的名字，无害
	}()
	if _, err := io.Copy(tmp, r); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, abs)
}

func (l *Local) Get(_ context.Context, key string) (io.ReadCloser, *ObjectInfo, error) {
	abs, err := l.abs(key)
	if err != nil {
		return nil, nil, err
	}
	f, err := os.Open(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	if st.IsDir() {
		_ = f.Close()
		return nil, nil, ErrNotFound // 不开目录列表
	}
	ct := mime.TypeByExtension(filepath.Ext(abs))
	if ct == "" {
		ct = "application/octet-stream"
	}
	return f, &ObjectInfo{Size: st.Size(), ContentType: ct}, nil
}

func (l *Local) Delete(_ context.Context, key string) error {
	abs, err := l.abs(key)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil // 删一个非法/不存在的 key 视为已删
		}
		return err
	}
	if err := os.Remove(abs); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
