package main

// 前后端的运单状态词表必须逐键一致。
//
// 由来：这两份表**本来就已经漂了**。后端有 partially_signed 和 rejected，
// 前端 STATUS_LABEL 里没有——而渲染处写的是 STATUS_LABEL[s] ?? s，
// 缺键就把 key 本身露出来。于是「部分签收」和「已拒收」的运单
// 在界面上显示成原始英文码：不报错、不崩，只是用户看到一串看不懂的英文。
//
// 后端那一侧刚从 7 份拷贝收成了 1 份，但跨语言这道边界收不掉：
// TypeScript 那份没法引用 Go 的常量。收不掉就只能盯着。
//
// 盯的方式是读 TS 源码取键名。这种检查有个众所周知的坏法——正则没匹配上时
// 悄悄扫出 0 个键然后绿灯放行，所以下面第一件事就是断言"确实扫到了东西"。

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/wbstatus"
)

func TestStatusLabelsMatchFrontend(t *testing.T) {
	const path = "../../../frontend/src/api/types.ts"
	src, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("读不到前端类型文件（%v），跳过——单独构建后端时没有前端源码", err)
	}

	// 截出 STATUS_LABEL 的对象字面量
	block := regexp.MustCompile(`(?s)export const STATUS_LABEL[^{]*\{(.*?)\n\};`).FindSubmatch(src)
	if block == nil {
		t.Fatal("在 types.ts 里没找到 STATUS_LABEL 的定义——" +
			"多半是改了写法，这条检查已经形同虚设，请一并更新正则")
	}
	keyRe := regexp.MustCompile(`(?m)^\s*([a-z_]+)\s*:\s*"`)
	front := map[string]bool{}
	for _, m := range keyRe.FindAllSubmatch(block[1], -1) {
		front[string(m[1])] = true
	}
	// 防空转：扫出 0 个键时这条检查永远是绿的，那种绿没有意义
	if len(front) == 0 {
		t.Fatal("从 STATUS_LABEL 里一个键都没扫到——正则失效了，这条检查正在空转")
	}

	var missing, extra []string
	for k := range wbstatus.Label {
		if !front[k] {
			missing = append(missing, k)
		}
	}
	for k := range front {
		if _, ok := wbstatus.Label[k]; !ok {
			extra = append(extra, k)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)

	if len(missing) > 0 {
		t.Errorf("前端 STATUS_LABEL 缺这些状态：%s\n"+
			"  渲染处是 STATUS_LABEL[s] ?? s，缺键会把原始英文码直接显示给用户。\n"+
			"  请在 frontend/src/api/types.ts 里补上。", strings.Join(missing, "、"))
	}
	if len(extra) > 0 {
		t.Errorf("前端 STATUS_LABEL 多出这些状态：%s\n"+
			"  后端已经不会再产生它们了，留着会让人以为系统还有这些状态。",
			strings.Join(extra, "、"))
	}
	if len(missing) == 0 && len(extra) == 0 {
		t.Logf("前后端状态词表一致，共 %d 个状态", len(front))
	}
}

// TestAbortedIsWiredEverywhere 新状态不能只加在状态表里。
//
// 加一个状态最容易漏的不是流转表本身，是那些**按状态判断**的地方：
// 有没有标签、算不算送达、算不算占用运力。漏一处的表现各不相同，
// 但共同点是都不报错。
func TestAbortedIsWiredEverywhere(t *testing.T) {
	if _, ok := wbstatus.Label[wbstatus.Aborted]; !ok {
		t.Error("aborted 没有中文标签，界面上会显示成英文码")
	}
	// 中止不是送达：不能进准班率的取样
	for _, s := range wbstatus.Delivered {
		if s == wbstatus.Aborted {
			t.Error("aborted 被算进了「已送达」——一趟没送到的运输会进准班率")
		}
	}
}

// TestReceiptStatusLabelsMatchFrontend 回单状态词表也得逐键一致。
//
// 这一对漂得比运单状态那次更彻底：后端签收时写 received、回单确认时写
// confirmed，前端只认 pending/returned/audited——**交集为空**。
// 于是一张运单只要走过签收，回单那一列就显示原始英文，
// 而「回单状态」筛选器里没有任何选项能选中它，那批单子筛不出来。
// 回单是回单付结算的前提，筛不出来就催不了款。
func TestReceiptStatusLabelsMatchFrontend(t *testing.T) {
	const path = "../../../frontend/src/api/types.ts"
	src, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("读不到前端类型文件（%v），跳过", err)
	}
	block := regexp.MustCompile(`(?s)export const RECEIPT_LABEL[^{]*\{(.*?)\n\};`).FindSubmatch(src)
	if block == nil {
		t.Fatal("在 types.ts 里没找到 RECEIPT_LABEL 的定义——" +
			"多半是改了写法或挪了地方，这条检查已经形同虚设")
	}
	keyRe := regexp.MustCompile(`([a-z_]+)\s*:\s*"`)
	front := map[string]bool{}
	for _, m := range keyRe.FindAllSubmatch(block[1], -1) {
		front[string(m[1])] = true
	}
	if len(front) == 0 {
		t.Fatal("从 RECEIPT_LABEL 里一个键都没扫到——这条检查正在空转")
	}

	var missing, extra []string
	for k := range wbstatus.ReceiptLabel {
		if !front[k] {
			missing = append(missing, k)
		}
	}
	for k := range front {
		if _, ok := wbstatus.ReceiptLabel[k]; !ok {
			extra = append(extra, k)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("前端 RECEIPT_LABEL 缺：%s（缺键会把英文码显示给用户）", strings.Join(missing, "、"))
	}
	if len(extra) > 0 {
		t.Errorf("前端 RECEIPT_LABEL 多出：%s（后端不会再产生这些值）", strings.Join(extra, "、"))
	}
}

// TestNoRawReceiptStatusLiteralsInSQL 回单状态不能再有第二处字面量写入。
//
// 四个写入点原先各写各的（三处 'received'、一处 'confirmed'），
// 而前端一个都不认识。收敛之后要防的是有人再手写一个新值进去——
// 那会立刻在界面上变回英文码，而且不报任何错。
func TestNoRawReceiptStatusLiteralsInSQL(t *testing.T) {
	roots := []string{"../../internal"}
	bad := []string{}
	for _, root := range roots {
		_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") {
				return nil
			}
			// 词表定义自己那一份不算
			if strings.Contains(p, "wbstatus") {
				return nil
			}
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				return nil
			}
			for _, m := range regexp.MustCompile(`receipt_status\s*=\s*'([a-z_]+)'`).FindAllStringSubmatch(string(b), -1) {
				bad = append(bad, p+" → receipt_status='"+m[1]+"'")
			}
			return nil
		})
	}
	if len(bad) > 0 {
		t.Errorf("这些地方直接把回单状态字面量写进了 SQL：\n  %s\n"+
			"  请改用 wbstatus.Receipt* 常量——前端词表靠它对齐，多一份就会漂。",
			strings.Join(bad, "\n  "))
	}
}
