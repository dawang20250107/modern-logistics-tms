package blob

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// FromEnv 按环境变量挑实现。
//
//	MEDIA_BACKEND=local（默认）  → 写 MEDIA_ROOT 目录
//	MEDIA_BACKEND=s3            → 需要 S3_ENDPOINT / S3_BUCKET / S3_REGION
//	                              / S3_ACCESS_KEY / S3_SECRET_KEY
//
// 配了 s3 却缺参数时**返回错误**，不静默退回本地盘。
// 退回去的话，多副本部署会"看起来正常"地跑起来，然后间歇性丢文件——
// 那比起不来难查得多。
func FromEnv() (Store, error) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("MEDIA_BACKEND"))) {
	case "", "local", "disk":
		root := os.Getenv("MEDIA_ROOT")
		if root == "" {
			root = "./media"
		}
		return NewLocal(root), nil
	case "s3", "cos", "oss", "minio":
		s := &S3{
			Endpoint:  strings.TrimSpace(os.Getenv("S3_ENDPOINT")),
			Region:    strings.TrimSpace(os.Getenv("S3_REGION")),
			Bucket:    strings.TrimSpace(os.Getenv("S3_BUCKET")),
			AccessKey: strings.TrimSpace(os.Getenv("S3_ACCESS_KEY")),
			SecretKey: strings.TrimSpace(os.Getenv("S3_SECRET_KEY")),
			PathStyle: os.Getenv("S3_PATH_STYLE") == "1",
			HTTP:      &http.Client{Timeout: 60 * time.Second},
		}
		var missing []string
		for _, kv := range []struct{ k, v string }{
			{"S3_ENDPOINT", s.Endpoint}, {"S3_REGION", s.Region}, {"S3_BUCKET", s.Bucket},
			{"S3_ACCESS_KEY", s.AccessKey}, {"S3_SECRET_KEY", s.SecretKey},
		} {
			if kv.v == "" {
				missing = append(missing, kv.k)
			}
		}
		if len(missing) > 0 {
			return nil, fmt.Errorf("MEDIA_BACKEND=s3 但缺少 %s；"+
				"不退回本地盘是有意的——多副本下退回去会间歇性丢文件，比起不来难查得多",
				strings.Join(missing, "、"))
		}
		if !strings.HasPrefix(s.Endpoint, "https://") && !strings.HasPrefix(s.Endpoint, "http://") {
			return nil, fmt.Errorf("S3_ENDPOINT 必须带协议头（https://…），实得 %q", s.Endpoint)
		}
		return s, nil
	default:
		return nil, fmt.Errorf("MEDIA_BACKEND 取值无法识别：%q（可用：local / s3）",
			os.Getenv("MEDIA_BACKEND"))
	}
}
