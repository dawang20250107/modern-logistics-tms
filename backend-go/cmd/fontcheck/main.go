// fontcheck 验证当前环境确实能渲染中文水印，不能就以非零码退出。
//
// 存在的理由：水印失败是静默的——没字体就原样返回照片，打卡照常成功，
// 只是从此不带时间/GPS/业务上下文。等到要拿它当证据时才发现，已经晚了。
// 镜像构建期跑一次，把"生产上没字体"从运行期的静默故障变成构建期的硬失败。
package main

import (
	"fmt"
	"os"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/driver"
)

func main() {
	p := driver.FontPath()
	if p == "" {
		fmt.Fprintln(os.Stderr,
			"未找到可渲染中文的字体：打卡水印会静默失效。请安装 CJK 字体（如 font-noto-cjk），"+
				"或用 WATERMARK_FONT 指定字体文件。")
		os.Exit(1)
	}
	fmt.Println("水印字体可用：" + p)
}
