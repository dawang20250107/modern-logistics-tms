package main

// 后台上传凭证：回单与司机证件。
//
// 这两条路径前端都是 multipart（WaybillDetailPage 传回单、FleetPage 传证件），
// 而通用 CRUD 引擎只会解 JSON——发过去直接 400「请求体不是合法 JSON」。
// 也就是说这两个功能在页面上是**按下去就报错**的。
//
// 司机自己那条路（/driver/credentials）另有实现、是好的，所以只看 App
// 不会发现；发现它要走一遍后台页面。

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

// postMultipart 传一个带文件的表单。
func (e *testEnv) postMultipart(token, path string, fields map[string]string, filename string, content []byte) *httptest.ResponseRecorder {
	e.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	if filename != "" {
		fw, err := mw.CreateFormFile("file", filename)
		if err != nil {
			e.t.Fatal(err)
		}
		if _, err := fw.Write(content); err != nil {
			e.t.Fatal(err)
		}
	}
	_ = mw.Close()
	req := httptest.NewRequest("POST", path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

// assertStoredAndFetchable 断言返回里的 file_display 能真的取回原字节。
func (e *testEnv) assertStoredAndFetchable(rec *httptest.ResponseRecorder, want []byte) {
	e.t.Helper()
	if rec.Code != http.StatusCreated {
		e.t.Fatalf("上传返回 %d：%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Data struct {
			File        *string `json:"file"`
			FileDisplay string  `json:"file_display"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		e.t.Fatalf("解析响应失败：%v — %s", err, rec.Body.String())
	}
	if out.Data.File == nil || *out.Data.File == "" {
		e.t.Fatalf("file 为空：字节没落盘（file_display=%q）", out.Data.FileDisplay)
	}
	// file_display 是前端直接塞进 <a href> 的值，必须是能打开的地址。
	// 只存了键名（receipts/xxx）而不带 /media/ 前缀的话，链接点开是 404——
	// 库里有文件、页面上打不开，和没存一样。
	got := e.call("", "GET", out.Data.FileDisplay, "")
	if got.Code != http.StatusOK {
		e.t.Fatalf("按 file_display=%q 取件 → %d，链接打不开", out.Data.FileDisplay, got.Code)
	}
	if !bytes.Equal(got.Body.Bytes(), want) {
		e.t.Errorf("取回的内容与上传的不一致")
	}
}

// TestReceiptUploadStoresFile 后台传回单：回单是签收凭证，丢了就没法证明货送到过。
func TestReceiptUploadStoresFile(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	wbID := e.mkWaybillRow()
	content := []byte("RECEIPT-BYTES-OK 签收回单扫描件")

	rec := e.postMultipart(token, "/api/v1/receipts",
		map[string]string{"waybill": wbID}, "回单.png", content)
	e.assertStoredAndFetchable(rec, content)
}

// TestDriverCredentialUploadStoresFile 后台传司机证件：驾驶证/道路运输证过期是要罚款的，
// 证件影像存不下来，合规检查就查不了。
func TestDriverCredentialUploadStoresFile(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	phone, _ := e.mkDriver()
	var drvID string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT id::text FROM md_driver WHERE phone=$1`, phone).Scan(&drvID); err != nil {
		t.Fatalf("取司机失败：%v", err)
	}
	content := []byte("CRED-BYTES-OK 驾驶证正面")

	// 字段照抄 FleetPage 真正发的那一组——包括 self_uploaded。
	// multipart 里所有值都是字符串，没有类型；只传一部分字段的话，
	// 布尔字段那条路根本走不到，用例绿了而页面上照样 400。
	rec := e.postMultipart(token, "/api/v1/driver-credentials",
		map[string]string{
			"driver": drvID, "cred_type": "driving_license", "side": "main",
			"self_uploaded": "false",
		},
		"driving.jpg", content)
	e.assertStoredAndFetchable(rec, content)
}

// TestJSONCreateStillWorksOnUploadResources 加了 multipart 支持之后，原来的 JSON 建档不能坏。
func TestJSONCreateStillWorksOnUploadResources(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	wbID := e.mkWaybillRow()
	rec := e.call(token, "POST", "/api/v1/receipts",
		`{"waybill":"`+wbID+`","file_url":"https://example.com/pod.png","signatory":"张三"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("JSON 建回单返回 %d：%s", rec.Code, rec.Body.String())
	}
}

// mkWaybillRow 造一张最简运单，返回 id。复用 abort_test 的建法：
// 那一份的列清单是照着真实表结构对出来的，另写一份只会再踩一次
// "列不存在"，而且失败时如果写成 Skip，用例就悄悄不跑了。
func (e *testEnv) mkWaybillRow() string {
	e.t.Helper()
	no := e.mkWaybillAt("dispatched")
	var id string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT id::text FROM ops_waybill WHERE waybill_no=$1`, no).Scan(&id); err != nil {
		e.t.Fatalf("取运单 id 失败：%v", err)
	}
	e.t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM ops_receipt WHERE waybill_id=$1::uuid`, id)
	})
	return id
}
