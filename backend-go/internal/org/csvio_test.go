package org

// 员工 CSV 导入的解码。
//
// 这一段值得单独测，因为它处理的是**别人用 Excel 导出来的文件**——
// 而 Excel 在中文 Windows 上默认导出的是 GBK，不是 UTF-8；
// 「CSV UTF-8」是另一个需要用户主动选的菜单项。
// 一个物流公司的人事名单，姓名、部门、职位全是中文。

import (
	"bytes"
	"encoding/csv"
	"io"
	"strings"
	"testing"
)

func readRows(t *testing.T, raw []byte) [][]string {
	t.Helper()
	cr := csv.NewReader(decodeCSV(bytes.NewReader(raw)))
	cr.FieldsPerRecord = -1
	var out [][]string
	for {
		row, err := cr.Read()
		if err != nil {
			break
		}
		out = append(out, row)
	}
	return out
}

// TestCSVDecodesUTF8AndBOM UTF-8（含 Excel 加的 BOM）要能正常读。
//
// BOM 那三个字节若不剥掉，第一列表头会变成 "\ufeff工号"（前面挂着一个不可见字符），
// 于是「第一行是不是表头」的判断落空，表头被当成一条真实员工记录导进去。
func TestCSVDecodesUTF8AndBOM(t *testing.T) {
	body := "工号,姓名,手机\nE002,李四,13900000000\n"
	for _, c := range []struct {
		name string
		raw  []byte
	}{
		{"纯 UTF-8", []byte(body)},
		{"UTF-8 带 BOM（Excel 导出的样子）", append([]byte{0xEF, 0xBB, 0xBF}, body...)},
	} {
		t.Run(c.name, func(t *testing.T) {
			rows := readRows(t, c.raw)
			if len(rows) != 2 {
				t.Fatalf("读到 %d 行，应为 2", len(rows))
			}
			if rows[0][0] != "工号" {
				t.Errorf("表头第一列是 %q，应为 工号（BOM 没剥干净会变成 \\ufeff工号）", rows[0][0])
			}
			if rows[1][1] != "李四" {
				t.Errorf("姓名读成 %q，应为 李四", rows[1][1])
			}
		})
	}
}

// TestCSVDecodesGBK 中文 Windows 上 Excel 默认导出的就是 GBK。
//
// 不转码的话，csv.Reader 会把 GBK 字节当 UTF-8 读，姓名变成一串乱码——
// 而且**不会报错**：导入照样返回"成功 N 条"，只是库里存的是乱码。
// 等有人翻员工名录才发现，那时候已经导进去几百条了。
func TestCSVDecodesGBK(t *testing.T) {
	// "工号,姓名,手机\nE001,张三,13800000000\n" 的 GBK 编码
	gbk := []byte{
		0xb9, 0xa4, 0xba, 0xc5, ',', 0xd0, 0xd5, 0xc3, 0xfb, ',', 0xca, 0xd6, 0xbb, 0xfa, '\n',
		'E', '0', '0', '1', ',', 0xd5, 0xc5, 0xc8, 0xfd, ',',
		'1', '3', '8', '0', '0', '0', '0', '0', '0', '0', '0', '\n',
	}
	rows := readRows(t, gbk)
	if len(rows) != 2 {
		t.Fatalf("读到 %d 行，应为 2", len(rows))
	}
	if rows[0][0] != "工号" {
		t.Errorf("表头读成 %q，应为 工号 —— GBK 没转码", rows[0][0])
	}
	if rows[1][1] != "张三" {
		t.Errorf("姓名读成 %q，应为 张三 —— GBK 没转码，会静默导进一批乱码", rows[1][1])
	}
}

// TestCSVKeepsASCIIIntact 纯 ASCII 不能被转码搞坏。
// 探测式解码最容易出的错就是"把本来好的也改坏了"。
func TestCSVKeepsASCIIIntact(t *testing.T) {
	raw := []byte("employee_no,name,phone\nE003,Zhang San,13700000000\n")
	rows := readRows(t, raw)
	if len(rows) != 2 || rows[1][1] != "Zhang San" {
		t.Errorf("ASCII 被改坏了：%v", rows)
	}
}

func TestPadAndBlank(t *testing.T) {
	// 列数不足要补空，不能越界 panic——手写的 CSV 经常少填后面几列
	if got := pad([]string{"a"}, 3); len(got) != 3 || got[0] != "a" || got[2] != "" {
		t.Errorf("pad 补位不对：%q", got)
	}
	// 列数超出按前 n 列取，多余的丢掉（记录现状）
	if got := pad([]string{"a", "b", "c", "d"}, 2); len(got) != 2 || got[1] != "b" {
		t.Errorf("pad 截断不对：%q", got)
	}
	if anyNonBlank([]string{"", "  ", "\t"}) {
		t.Error("全空白行应判为空行——否则文件末尾的空行会被当成一条记录")
	}
	if !anyNonBlank([]string{"", "x"}) {
		t.Error("有内容的行被判成了空行")
	}
}

// bomReader 的直接用例：短于 3 字节的输入不能 panic
func TestBomReaderShortInput(t *testing.T) {
	for _, s := range []string{"", "a", "ab", "abc"} {
		b, err := io.ReadAll(bomReader(strings.NewReader(s)))
		if err != nil {
			t.Fatalf("%q 读失败：%v", s, err)
		}
		if string(b) != s {
			t.Errorf("%q 被改成了 %q", s, string(b))
		}
	}
}
