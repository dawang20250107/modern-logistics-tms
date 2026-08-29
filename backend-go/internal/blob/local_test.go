package blob

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustPut(t *testing.T, s Store, key, body string) {
	t.Helper()
	if err := s.Put(context.Background(), key, strings.NewReader(body), int64(len(body)), "text/plain"); err != nil {
		t.Fatalf("Put %s 失败：%v", key, err)
	}
}

func readAll(t *testing.T, s Store, key string) string {
	t.Helper()
	rc, _, err := s.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get %s 失败：%v", key, err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("读 %s 失败：%v", key, err)
	}
	return string(b)
}

func TestLocalRoundTrip(t *testing.T) {
	s := NewLocal(t.TempDir())
	mustPut(t, s, "avatars/a.png", "hello")
	if got := readAll(t, s, "avatars/a.png"); got != "hello" {
		t.Errorf("读回 %q，期望 hello", got)
	}
	// 覆盖写
	mustPut(t, s, "avatars/a.png", "world")
	if got := readAll(t, s, "avatars/a.png"); got != "world" {
		t.Errorf("覆盖后读回 %q，期望 world", got)
	}
	if err := s.Delete(context.Background(), "avatars/a.png"); err != nil {
		t.Fatalf("Delete 失败：%v", err)
	}
	if _, _, err := s.Get(context.Background(), "avatars/a.png"); !errors.Is(err, ErrNotFound) {
		t.Errorf("删后 Get 应回 ErrNotFound，实得 %v", err)
	}
}

// TestLocalDeleteIsIdempotent 删一个本就不存在的对象不能报错。
//
// 「换头像时把旧文件删掉」这类清理动作，旧文件可能早就不在了
// （上一次删过、手工清过、或者根本没上传成功）。删除不幂等的话，
// 整个换头像请求会因为一个无关紧要的清理失败而 500。
func TestLocalDeleteIsIdempotent(t *testing.T) {
	s := NewLocal(t.TempDir())
	for i := 0; i < 2; i++ {
		if err := s.Delete(context.Background(), "nope/missing.png"); err != nil {
			t.Fatalf("第 %d 次删不存在的对象报错：%v", i+1, err)
		}
	}
}

// TestLocalRejectsPathTraversal key 里带 .. 不许读写到根目录外面。
//
// key 有一部分来自请求路径（/media/<key>）。原先这条路由靠
// http.FileServer 自带的清理挡着；抽象成 Store 之后那层保障就没了。
// 换实现时最容易丢的恰恰是这种「本来由框架顺手提供」的保护——
// 它不会编译报错，也不会在正常用例里暴露。
func TestLocalRejectsPathTraversal(t *testing.T) {
	root := t.TempDir()
	// 在 root 外面放一个"机密文件"，确认穿越读不到它
	outside := filepath.Join(filepath.Dir(root), "outside-secret.txt")
	if err := os.WriteFile(outside, []byte("SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(outside) })

	s := NewLocal(root)
	for _, key := range []string{
		"../outside-secret.txt",
		"../../etc/passwd",
		"avatars/../../outside-secret.txt",
		"/../outside-secret.txt",
		"..",
	} {
		t.Run(key, func(t *testing.T) {
			rc, _, err := s.Get(context.Background(), key)
			if err == nil {
				b, _ := io.ReadAll(rc)
				rc.Close()
				t.Fatalf("穿越 key %q 竟然读到了内容：%q", key, string(b))
			}
			if err := s.Put(context.Background(), key, strings.NewReader("x"), 1, ""); err == nil {
				t.Errorf("穿越 key %q 竟然写成功了", key)
			}
		})
	}
	// 确认那个文件没被上面的 Put 覆盖
	b, err := os.ReadFile(outside)
	if err != nil || string(b) != "SECRET" {
		t.Errorf("root 外的文件被动了：%q %v", string(b), err)
	}
}

// TestLocalNoDirectoryListing 拿目录当 key 读，必须是 ErrNotFound 而不是列目录。
func TestLocalNoDirectoryListing(t *testing.T) {
	s := NewLocal(t.TempDir())
	mustPut(t, s, "certs/driver/id.png", "x")
	for _, key := range []string{"certs", "certs/", "certs/driver"} {
		if _, _, err := s.Get(context.Background(), key); !errors.Is(err, ErrNotFound) {
			t.Errorf("Get 目录 %q 应回 ErrNotFound，实得 %v", key, err)
		}
	}
}

// TestLocalPutIsAtomic 写到一半失败时，不能留下一个「半张图」。
//
// 直接 os.Create + io.Copy 的话，读到一半的 reader 报错会在目标路径上
// 留下一个大小对不上的文件，而它看起来完全正常——后面谁读到都以为是完整的。
// 实现里用的是「先写临时文件再 rename」，这条钉住它。
func TestLocalPutIsAtomic(t *testing.T) {
	root := t.TempDir()
	s := NewLocal(root)
	failing := io.MultiReader(strings.NewReader("head"), &errReader{})
	if err := s.Put(context.Background(), "x/broken.bin", failing, -1, ""); err == nil {
		t.Fatal("Put 应当把 reader 的错误透出来")
	}
	if _, _, err := s.Get(context.Background(), "x/broken.bin"); !errors.Is(err, ErrNotFound) {
		t.Errorf("写失败后不该留下对象，实得 %v", err)
	}
	// 临时文件也不该留下
	ents, _ := os.ReadDir(filepath.Join(root, "x"))
	for _, e := range ents {
		t.Errorf("残留了文件：%s", e.Name())
	}
}

type errReader struct{}

func (*errReader) Read([]byte) (int, error) { return 0, errors.New("读取中断") }

// TestLocalContentType 按扩展名给 Content-Type。
// 浏览器拿不到正确类型时会去猜，而这些是用户上传物——不能让它猜。
func TestLocalContentType(t *testing.T) {
	s := NewLocal(t.TempDir())
	for _, c := range []struct{ key, want string }{
		{"a/x.png", "image/png"},
		{"a/x.jpg", "image/jpeg"},
		{"a/x.unknownext", "application/octet-stream"},
	} {
		mustPut(t, s, c.key, "x")
		_, info, err := s.Get(context.Background(), c.key)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(info.ContentType, c.want) {
			t.Errorf("%s 的 Content-Type 是 %q，期望以 %q 打头", c.key, info.ContentType, c.want)
		}
	}
}

func TestLocalSizeReported(t *testing.T) {
	s := NewLocal(t.TempDir())
	body := bytes.Repeat([]byte("z"), 1234)
	if err := s.Put(context.Background(), "a/b.bin", bytes.NewReader(body), int64(len(body)), ""); err != nil {
		t.Fatal(err)
	}
	_, info, err := s.Get(context.Background(), "a/b.bin")
	if err != nil {
		t.Fatal(err)
	}
	if info.Size != 1234 {
		t.Errorf("Size=%d，期望 1234", info.Size)
	}
}

// TestLocalRejectsAliasingKeys 需要"清理"才合法的 key 一律拒绝，而不是悄悄清理后接受。
//
// 这一条跟上面那条穿越用例管的不是同一件事：
// 穿越用例盯的是**越权**（读到 root 外面），由前缀兜底挡住；
// 这一条盯的是**别名**——"./x" 与 "x"、"a//b" 与 "a/b" 若都能用，
// 同一个文件就有多个 key，而 /media/./x 会正常返回内容而不是 404。
// 需要被清理的 key，本来就是调用方没打算要的 key。
func TestLocalRejectsAliasingKeys(t *testing.T) {
	s := NewLocal(t.TempDir())
	mustPut(t, s, "a/b.png", "real")

	for _, key := range []string{
		"./a/b.png",
		"a//b.png",
		"a/./b.png",
		`a\b.png`, // 反斜杠在 Windows 上是分隔符，key 约定只用正斜杠
	} {
		t.Run(key, func(t *testing.T) {
			if _, _, err := s.Get(context.Background(), key); !errors.Is(err, ErrNotFound) {
				t.Errorf("别名 key %q 应回 ErrNotFound，实得 %v —— "+
					"接受它等于让一个文件有多个 key", key, err)
			}
		})
	}
	// 正规写法仍然可读
	if got := readAll(t, s, "a/b.png"); got != "real" {
		t.Errorf("正规 key 读回 %q", got)
	}
}
