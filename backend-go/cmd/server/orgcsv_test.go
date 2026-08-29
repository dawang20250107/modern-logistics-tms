package main

// 员工 CSV 的导入导出往返。
//
// 这是客户上线第一天要走的路：导出模板 → 在 Excel 里填 → 传回来。
// 中间任何一环坏了，客户连人都建不进来。
//
// 单独走 HTTP 而不是只测 decodeCSV，是因为今天已经吃过一次亏：
// 解码函数的单测是绿的，而真实路径（前端发 multipart、后端只解 JSON）直接 400。
// 编解码对不代表这条路通。
//
// GBK 那一条尤其要在这一层测：国内 Excel 另存 CSV 默认就是 GBK，
// 不转码的后果不是报错，是**静默导进一批乱码**——名字全变成问号，
// 而接口回的是 created: N，看起来一切正常。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"golang.org/x/text/encoding/simplifiedchinese"
)

func (e *testEnv) importEmployees(token string, content []byte) (created, updated int, errs []any, code int) {
	e.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "employees.csv")
	_, _ = fw.Write(content)
	_ = mw.Close()
	req := httptest.NewRequest("POST", "/api/v1/org/employees/import", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	var out struct {
		Data struct {
			Created int   `json:"created"`
			Updated int   `json:"updated"`
			Errors  []any `json:"errors"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out.Data.Created, out.Data.Updated, out.Data.Errors, rec.Code
}

// orgCode 取一个可用的组织编码，并在用例结束时清掉造出来的员工。
func (e *testEnv) orgCode(prefix string) string {
	e.t.Helper()
	var code string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT code FROM iam_organization ORDER BY sort_order, code LIMIT 1`).Scan(&code); err != nil {
		e.t.Skipf("库里没有组织，跳过：%v", err)
	}
	e.t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(),
			`DELETE FROM iam_employee WHERE employee_no LIKE $1`, prefix+"%")
	})
	return code
}

// TestEmployeeCSVRoundTrip 导入 → 导出 → 原样回灌，三步都不能丢东西。
func TestEmployeeCSVRoundTrip(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	tag := "T" + strings.ToUpper(uuid.NewString()[:6])
	code := e.orgCode(tag)

	csv := fmt.Sprintf("\ufeff工号,姓名,手机,组织编码,职位,直接上级工号,状态\n"+
		"%s1,张三,13800000001,%s,调度员,,在职\n"+
		"%s2,李四,13800000002,%s,客服,%s1,在职\n", tag, code, tag, code, tag)

	created, _, errs, sc := e.importEmployees(token, []byte(csv))
	if sc != http.StatusOK {
		t.Fatalf("导入返回 %d", sc)
	}
	if created != 2 || len(errs) > 0 {
		t.Fatalf("导入应新建 2 人无报错，实际 created=%d errors=%v", created, errs)
	}

	// 上下级要真的接上。两遍处理的意义就在这里：第一遍建人时上级还不存在。
	var sup string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT COALESCE(s.employee_no,'') FROM iam_employee e
		 LEFT JOIN iam_employee s ON s.id = e.supervisor_id
		 WHERE e.employee_no = $1`, tag+"2").Scan(&sup); err != nil {
		t.Fatalf("查上级失败：%v", err)
	}
	if sup != tag+"1" {
		t.Errorf("直接上级没接上：期望 %s1，实际 %q —— 表里有人但汇报线是断的", tag, sup)
	}

	// 导出必须包含刚导入的两个人，且中文没坏
	rec := e.call(token, "GET", "/api/v1/org/employees/export", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("导出返回 %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.HasPrefix(body, "\ufeff") {
		t.Error("导出没有 UTF-8 BOM —— 国内用 Excel 打开会看到乱码表头")
	}
	for _, want := range []string{tag + "1,张三", tag + "2,李四"} {
		if !strings.Contains(body, want) {
			t.Errorf("导出里找不到 %q", want)
		}
	}

	// 原样回灌：应当全是更新，一条都不该重复新建
	created2, updated2, errs2, _ := e.importEmployees(token, []byte(body))
	if created2 != 0 {
		t.Errorf("把导出的文件原样传回去又新建了 %d 人 —— 客户按「导出改一改再传回来」"+
			"这个最自然的用法操作，人就会翻倍", created2)
	}
	if updated2 < 2 || len(errs2) > 0 {
		t.Errorf("回灌应更新 ≥2 人且无报错，实际 updated=%d errors=%v", updated2, errs2)
	}
}

// TestEmployeeCSVAcceptsGBK 国内 Excel 另存 CSV 默认是 GBK。
//
// 不转码的后果不是报错，是静默导进一批乱码：接口照样回 created: N，
// 名字却全成了问号，而客户此时已经把表关了。
func TestEmployeeCSVAcceptsGBK(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	tag := "G" + strings.ToUpper(uuid.NewString()[:6])
	code := e.orgCode(tag)

	utf8CSV := fmt.Sprintf("工号,姓名,手机,组织编码,职位,直接上级工号,状态\n"+
		"%s1,王五,13800000003,%s,调度主管,,在职\n", tag, code)
	gbk, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(utf8CSV))
	if err != nil {
		t.Fatalf("转 GBK 失败：%v", err)
	}

	created, _, errs, sc := e.importEmployees(token, gbk)
	if sc != http.StatusOK || created != 1 || len(errs) > 0 {
		t.Fatalf("GBK 导入失败：code=%d created=%d errors=%v", sc, created, errs)
	}

	var name, pos string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT name, position FROM iam_employee WHERE employee_no=$1`, tag+"1").Scan(&name, &pos); err != nil {
		t.Fatalf("查人失败：%v", err)
	}
	// 这里必须逐字比对，不能只看"导进来了几条"——
	// 乱码的那种坏法条数是对的，内容才是错的。
	if name != "王五" || pos != "调度主管" {
		t.Errorf("GBK 内容没还原：name=%q position=%q（期望 王五 / 调度主管）—— "+
			"客户会得到一批名字是问号的员工，而接口说导入成功了", name, pos)
	}
}
