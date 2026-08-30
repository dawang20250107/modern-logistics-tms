package httpx

import (
	"encoding/csv"
	"net/http"
	"strconv"
)

// ExportMaxRows CSV 导出的行数上限。
//
// 上限本身是必要的：没有上限时一次导出会把整张表拉进内存、
// 并长时间占住一条连接。问题不在于有上限，而在于**超了不说**。
//
// 原先三个导出端点各自写死 LIMIT 5000 且悄悄截断：5 万单的库里点「导出全部」，
// 拿到的是最近 5000 条——表头齐、行也齐、文件打得开，只是少了 90%。
// 拿去对账或报税，差额没人对得出来。这套系统在调度台上栽过同一条
// （把 8336 说成 20）：安静地把一部分当成全部。
const ExportMaxRows = 50000

// sanitizeCell 挡住 Excel 公式注入。
//
// CSV 本身没问题：encoding/csv 会把逗号、引号、换行正确转义。
// 问题在**打开它的那个程序**——Excel/WPS 会把以 = + @ 开头的单元格当公式执行。
// 而这套系统的导出正是给财务用 Excel 打开的，客户名、备注、地址
// 又都来自用户输入（其中"自助下单"那条路还是对公网开放的）。
//
// 实测：把订单目的地填成 `=1+1`，导出的 CSV 里就是原样的 `=1+1`，
// 财务一打开就变成 2。换成 `=HYPERLINK(...)` 或
// `=cmd|'/c calc'!A1` 这类，性质就完全不同了。
//
// 处理办法是业界通用的：在前面加一个单引号，Excel 会把整格当文本。
// 减号要单独判——**负数金额不能被误伤**：`-1200.50` 加了引号就从数字变成
// 文本，财务那一列就再也求不了和了。所以只有"以减号开头且不是数字"才处理。
func sanitizeCell(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '@', '\t', '\r':
		return "'" + s
	case '-':
		if _, err := strconv.ParseFloat(s, 64); err == nil {
			return s // 真的是负数，原样留着
		}
		return "'" + s
	}
	return s
}

func sanitizeRow(rec []string) []string {
	out := make([]string, len(rec))
	for i, v := range rec {
		out[i] = sanitizeCell(v)
	}
	return out
}

// ExportWriter 统一的导出写法：BOM + 表头，并在截断时留下痕迹。
type ExportWriter struct {
	cw        *csv.Writer
	cols      int
	n         int
	total     int // 符合条件的总行数；<0 表示未知
	truncated bool
}

// NewExport 起一次 CSV 导出。
//
// total 是符合筛选条件的总行数（调用方先 count 一次），用来判断会不会截断——
// 必须在写第一个字节之前知道，否则响应头已经发出去了，X-Export-Truncated
// 再也加不上。传 <0 表示未知，此时只按实际写入行数是否顶到上限来判断。
//
// 写 UTF-8 BOM 是因为国内多数人用 Excel 打开 CSV：没有 BOM 的话中文表头
// 会变成乱码——文件没错，但用户会以为导出坏了。
func NewExport(w http.ResponseWriter, filename string, header []string, total int) *ExportWriter {
	e := &ExportWriter{cols: len(header), total: total}
	e.truncated = total > ExportMaxRows
	w.Header().Set("Content-Type", "text/csv; charset=utf-8-sig")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("X-Export-Max-Rows", strconv.Itoa(ExportMaxRows))
	if e.truncated {
		w.Header().Set("X-Export-Truncated", "1")
	}
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})
	e.cw = csv.NewWriter(w)
	_ = e.cw.Write(sanitizeRow(header))
	return e
}

// Row 写一行；返回 false 表示已到上限，调用方应停止取数。
func (e *ExportWriter) Row(rec []string) bool {
	if e.n >= ExportMaxRows {
		e.truncated = true
		return false
	}
	_ = e.cw.Write(sanitizeRow(rec))
	e.n++
	return true
}

// Done 收尾：截断时在文件**最后一行**写明。
//
// 只靠响应头是不够的——用户拿到手的是一个 .csv 文件，头在下载那一刻就没了。
// 唯一到得了人眼前的地方，是文件自己的最后一行。
func (e *ExportWriter) Done() {
	if e.truncated {
		note := make([]string, e.cols)
		note[0] = "导出已截断"
		if e.cols > 1 {
			msg := "本次仅导出前 " + strconv.Itoa(e.n) + " 行"
			if e.total > 0 {
				msg += "，符合条件的共 " + strconv.Itoa(e.total) + " 行"
			}
			note[1] = msg + "。请用筛选条件缩小范围后分批导出。"
		}
		_ = e.cw.Write(note)
	}
	e.cw.Flush()
}

// Limit 供 SQL 用的 LIMIT 值。
func ExportLimit() int { return ExportMaxRows }
