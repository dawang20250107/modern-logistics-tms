package main

// 订单附件的上传闭环。
//
// 合同、磅单、回单是这门生意的凭证——对账吵起来的时候，拿得出来的就是它们。
// 所以"上传"这件事不能只把文件名写进库：附件行在、名字对、点开是 404，
// 等同于没有凭证，而且比没上传更坏——用户以为存下了，不会再去补。
//
// 这一组钉的就是**字节真的落了盘、并且能从 /media/ 取回来**，
// 而不只是接口返回了 201。

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// uploadAttachment 用真正的 multipart 表单传一个文件，返回响应。
func (e *testEnv) uploadAttachment(token, orderID, filename string, content []byte) *httptest.ResponseRecorder {
	e.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := fw.Write(content); err != nil {
		e.t.Fatal(err)
	}
	_ = mw.WriteField("kind", "contract")
	_ = mw.Close()

	req := httptest.NewRequest("POST", "/api/v1/orders/"+orderID+"/attachments", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

// TestOrderAttachmentUploadPersistsBytes 传上去的文件必须真的能取回来。
//
// 修之前的行为：接口读了 multipart 里的**文件名**，把字节丢了，
// 库里 file=” ，前端拿到的 file_display 是空串——那一行渲染成不可点的纯文字，
// 而 toast 还是"附件已上传"。传合同的人不会发现，直到对账要证据的那天。
func TestOrderAttachmentUploadPersistsBytes(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	orderID := e.mkOrder()
	content := []byte("这是一份合同扫描件的内容 CONTRACT-BYTES-OK")

	rec := e.uploadAttachment(token, orderID, "合同.txt", content)
	if rec.Code != http.StatusCreated {
		t.Fatalf("上传返回 %d：%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Data struct {
			File        *string `json:"file"`
			FileDisplay string  `json:"file_display"`
			Name        string  `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析响应失败：%v — %s", err, rec.Body.String())
	}

	if out.Data.File == nil || *out.Data.File == "" {
		t.Fatalf("附件的 file 是空的（file_display=%q）—— 只记了文件名，字节没存",
			out.Data.FileDisplay)
	}
	if out.Data.FileDisplay == "" {
		t.Fatal("file_display 为空，前端那一行会渲染成不可点的纯文字")
	}

	// 真的从 /media/ 取回来，并逐字节比对。
	// 只断言"库里有路径"是不够的：路径写对了、文件没落盘，照样取不出来。
	got := e.call("", "GET", out.Data.FileDisplay, "")
	if got.Code != http.StatusOK {
		t.Fatalf("取件 %s → %d，附件行在但打不开", out.Data.FileDisplay, got.Code)
	}
	if !bytes.Equal(got.Body.Bytes(), content) {
		t.Errorf("取回来的内容和传上去的不一致：%q", got.Body.String())
	}
}

// TestOrderAttachmentKeepsExternalURL 传外链（只给 file_url、不带文件）的老用法不能被打断。
func TestOrderAttachmentKeepsExternalURL(t *testing.T) {
	e := newTestEnv(t)
	token := e.mkUser(true)
	orderID := e.mkOrder()

	rec := e.call(token, "POST", "/api/v1/orders/"+orderID+"/attachments",
		`{"kind":"contract","name":"外部合同","file_url":"https://example.com/c.pdf"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("外链附件返回 %d：%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Data struct {
			FileDisplay string `json:"file_display"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析响应失败：%v — %s", err, rec.Body.String())
	}
	if out.Data.FileDisplay != "https://example.com/c.pdf" {
		t.Errorf("外链附件的 file_display 应回落到 file_url，实际 %q", out.Data.FileDisplay)
	}
}

// mkOrder 造一张最简订单，返回 id。
func (e *testEnv) mkOrder() string {
	e.t.Helper()
	id := uuid.NewString()
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO ops_order (id, created_at, updated_at, order_no, source, status, remark,
		  cargo_desc, cargo_quantity, cargo_volume_cbm, cargo_weight_ton, channel,
		  contact_name, contact_phone, destination, origin, parse_meta, raw_text,
		  business_type, cargo_value, delivery_address, delivery_contact_name,
		  delivery_contact_phone, is_deleted, is_hazardous, package_type, pickup_address,
		  pickup_contact_name, pickup_contact_phone, priority, quoted_amount, settlement_type,
		  source_type, temperature_range, sla_status, approval_remark, approval_status,
		  ai_conversation_id, cod_amount, cod_status, freight_payer, freight_term)
		VALUES ($1::uuid, now(), now(), $2, 'cs', 'pooled', '', '测试货', 1, 1, 1, 'cs',
		  '张三', '13800000000', '上海', '杭州', '{}'::jsonb, '', 'ftl', 0, '收货地址', '李四', '13900000000',
		  false, false, '纸箱', '发货地址', '王五', '13700000000', 'normal', 0, 'monthly',
		  'enterprise', '', 'pending', '', 'none', '', 0, 'none', 'consignor', 'prepaid')`,
		id, "ATT"+uuid.NewString()[:10]); err != nil {
		e.t.Fatalf("造订单失败：%v", err)
	}
	e.t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM ops_order WHERE id=$1::uuid`, id)
	})
	return id
}
