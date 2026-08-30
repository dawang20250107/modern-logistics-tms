package driver

// 打卡照片是这套系统在争议时唯一拿得出的现场证据：拍摄时间、GPS、
// 节点·司机·运单号都焊进像素里，"事后无法靠改库洗掉"是它的全部价值。
//
// 这条用例钉住两件事，都是真按了按钮之后回库核对才发现的：
//
//  1. 同一节点打两次卡，两张照片必须是两个文件。
//     原先路径写死成 "<运单号>_<节点>.jpg"，第二次**原地覆盖**第一次。
//     而弱网重试正是常规路径——司机端界面上就有一颗"重试"按钮。
//     实测连打两次：库里两行打卡记录，photo 指向同一个文件，
//     第一行拿到的是第二张照片，时间、GPS、节点全是第二次的。
//     一次普通重试就把"焊进像素"洗掉了。
//
//  2. 照片存储失败时接口必须说实话。
//     原先 `if err := saveMedia(...); err == nil { photoRel = rel }`——
//     错误被直接吞掉，接口照样 201 `{"ok":true}`，司机以为照片交了，
//     库里 photo 是 NULL。和这一轮查出来的三条上传路径是同一种坏法。
//
// 需要真实 Postgres；无 DATABASE_URL 则跳过。

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/migrate"
)

// jpegBytes 造一张真能被 image.Decode 解开的 JPEG。
//
// 不能随手塞几个字节：Watermark 解不开图时会**原样返回**，
// 于是"水印没打上"这个断言会永远红，而红的原因是样本坏了不是产品坏了。
// 走查脚本那边就先踩过一次（手写的 base64 是截断的）。
func jpegBytes(t *testing.T, c color.RGBA, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			// 加噪声，免得压得太狠、打没打水印看不出差别
			img.Set(x, y, color.RGBA{c.R ^ uint8(x), c.G ^ uint8(y), c.B, 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 92}); err != nil {
		t.Fatalf("造样本图失败：%v", err)
	}
	return buf.Bytes()
}

func checkinForm(t *testing.T, token, wbNo, node string, photo []byte) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range map[string]string{
		"token": token, "waybill_no": wbNo, "node": node, "lat": "31.23", "lng": "121.47",
	} {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	fw, err := mw.CreateFormFile("photo", "p.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(photo); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return &body, mw.FormDataContentType()
}

func TestCheckinPhotoIsNotOverwrittenByRetry(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("未设置 DATABASE_URL，跳过打卡照片测试")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("连库失败：%v", err)
	}
	// 用 t.Cleanup 而不是 defer：清理数据的 Cleanup 也要用这个池，
	// 而 defer 在函数返回时就跑，比 Cleanup 早——池先关了，清理全部失败。
	// Cleanup 是后进先出，这里先注册关池，它就最后跑。
	//
	// 原先这段清理写的是 `_, _ = pool.Exec(...)`，错误被吞掉，
	// 于是"清理失败"这件事本身没人知道，用例每跑一次库里多一份垃圾。
	// 和这一轮修的那些 200-但什么都没发生是同一种坏法。
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("库 ping 不通：%v", err)
	}
	if err := migrate.Run(ctx, pool); err != nil {
		t.Fatalf("迁移失败：%v", err)
	}

	sfx := uuid.NewString()[:8]
	drvID, wbID := uuid.New(), uuid.New()
	wbNo := "CKT-" + sfx
	idNo := "310101199001010011"
	phone := "139" + sfx[:8]
	// 这两张表上一批 NOT NULL 列都没有默认值，少一列整条 INSERT 就报错。
	// 这里把它们显式写全，而不是靠"应该有默认值吧"。
	if _, err := pool.Exec(ctx, `INSERT INTO md_driver
		(id, created_at, updated_at, is_deleted, name, phone, id_no, license_no, is_active,
		 license_type, qualification_cert_no, employment_type, app_registered,
		 cumulative_freight, cumulative_waybills, wechat)
		VALUES ($1, now(), now(), false, $2, $3, $4, 'A2-'||$5, true,
		        'A2', '', 'employee', false, 0, 0, '')`,
		drvID, "打卡用例司机", phone, idNo, sfx); err != nil {
		t.Fatalf("造司机失败：%v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO ops_waybill
		(id, created_at, updated_at, waybill_no, route_name, origin, destination, status,
		 dispatch_status, risk_level, receipt_status, eta_drift_minutes, cargo_quantity,
		 cargo_weight_ton, cargo_volume_cbm, dispatch_type, ai_conversation_id, cod_amount,
		 cod_status, freight_payer, freight_term, platform_name, platform_order_no, driver_id)
		VALUES ($1, now(), now(), $2, '打卡用例线路', '上海', '南京', 'departed',
		        'dispatched', 'low', 'pending', 0, 1, 1.0, 1.0, 'self', '', 0,
		        'none', 'shipper', 'prepaid', '', '', $3)`, wbID, wbNo, drvID); err != nil {
		t.Fatalf("造运单失败：%v", err)
	}
	// 打卡会推进运单状态，顺带写 ops_waybill_event——清理时先删它，
	// 否则外键挡住删不掉运单，用例每跑一次就在库里留一份垃圾。
	t.Cleanup(func() {
		for _, sql := range []string{
			`DELETE FROM ops_waybill_event WHERE waybill_id=$1`,
			`DELETE FROM ops_driver_checkin WHERE waybill_id=$1`,
			`DELETE FROM ops_waybill WHERE id=$1`,
		} {
			if _, err := pool.Exec(ctx, sql, wbID); err != nil {
				t.Logf("清理失败（%s）：%v", sql, err)
			}
		}
		if _, err := pool.Exec(ctx, `DELETE FROM md_driver WHERE id=$1`, drvID); err != nil {
			t.Logf("清理司机失败：%v", err)
		}
	})

	media := t.TempDir()
	h := &Handler{DB: pool, Secret: "test-insecure-secret-min-32-bytes-long!!", MediaRoot: media}
	token := SignToken(h.Secret, drvID.String())

	shots := [][]byte{
		jpegBytes(t, color.RGBA{200, 40, 40, 255}, 320, 240),
		jpegBytes(t, color.RGBA{40, 200, 40, 255}, 320, 240),
	}
	for i, shot := range shots {
		body, ct := checkinForm(t, token, wbNo, "in_transit", shot)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/driver/checkin", body)
		req.Header.Set("Content-Type", ct)
		rec := httptest.NewRecorder()
		h.Checkin(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("第 %d 次打卡返回 %d：%s", i+1, rec.Code, rec.Body.String())
		}
	}

	rows, err := pool.Query(ctx, `SELECT coalesce(photo,'') FROM ops_driver_checkin
		WHERE waybill_id=$1 AND node='in_transit' ORDER BY created_at`, wbID)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	rows.Close()

	if len(paths) != 2 {
		t.Fatalf("同一节点打了两次卡，库里应该有 2 行，实际 %d 行", len(paths))
	}
	for i, p := range paths {
		if p == "" {
			t.Fatalf("第 %d 行的 photo 是空的——照片传上去了但没落库", i+1)
		}
	}
	if paths[0] == paths[1] {
		t.Fatalf("两次打卡的照片指向同一个文件 %q：\n"+
			"  第二次会把第一次的原地覆盖，第一行记录拿到的是第二张照片，\n"+
			"  水印上的时间/GPS/节点全是第二次的——弱网重试一次就把证据洗掉了。", paths[0])
	}

	// 两个文件都要在、内容要不同、而且都不能等于原图（等于原图 = 水印那步没生效）
	var got [][]byte
	for i, p := range paths {
		b, err := os.ReadFile(media + "/" + p)
		if err != nil {
			t.Fatalf("第 %d 张照片取不回来：%v", i+1, err)
		}
		if bytes.Equal(b, shots[i]) {
			t.Errorf("第 %d 张和原图一模一样——水印没打上（没字体？图没解码？），凭证等于一张裸照片", i+1)
		}
		if _, _, err := image.Decode(bytes.NewReader(b)); err != nil {
			t.Errorf("第 %d 张存下来的不是能解码的图：%v", i+1, err)
		}
		got = append(got, b)
	}
	if bytes.Equal(got[0], got[1]) {
		t.Error("两个文件路径不同但内容一样——覆盖发生在别的地方")
	}
}

// 司机端任务列表必须封顶，而且要把真实总数带出来。
//
// 原先不限条数。演示库里一个司机有 3032 张在途单，实测一次 /driver/tasks：
//
//	1.19 MB JSON，前端把 3032 张卡片全渲染出来，页面高 1,140,794 px
//	（视口 844 px，也就是 1351 屏），登录到出卡片 6.2 秒。
//
// 这是**手机上**的页面：那 1.19 MB 走的是司机的流量，而他要找的是下一单在哪。
//
// 封顶之后必须同时给出总数，否则界面上会写「50 单进行中」——
// 那就是"把一页当全量"，这套系统已经在导出、调度池、登录审计、
// 待核销队列上犯过四次。
func TestDriverTaskListIsCappedAndReportsTrueTotal(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("未设置 DATABASE_URL，跳过司机任务列表测试")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("连库失败：%v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("库 ping 不通：%v", err)
	}
	if err := migrate.Run(ctx, pool); err != nil {
		t.Fatalf("迁移失败：%v", err)
	}

	// 找一个在途单数量超过上限的司机；没有就说清楚跳过的理由，
	// 而不是让这条用例在一个 3 单的司机身上"通过"。
	var drvID string
	var total int
	err = pool.QueryRow(ctx, `SELECT driver_id::text, count(*) FROM ops_waybill
		WHERE driver_id IS NOT NULL AND status IN ('dispatched','loaded','departed','in_transit','arrived')
		GROUP BY driver_id HAVING count(*) > $1 ORDER BY count(*) DESC LIMIT 1`, driverTaskLimit).
		Scan(&drvID, &total)
	if err != nil {
		t.Skipf("库里没有在途单超过 %d 的司机，这条用例这次没验到截断（跑过 seed 的库通常有）", driverTaskLimit)
	}

	const secret = "test-insecure-secret-min-32-bytes-long!!"
	h := &Handler{DB: pool, Secret: secret, MediaRoot: t.TempDir()}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/driver/tasks", nil)
	req.Header.Set("X-Driver-Token", SignToken(secret, drvID))
	rec := httptest.NewRecorder()
	h.Tasks(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("取任务返回 %d：%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Data struct {
			Waybills     []map[string]any `json:"waybills"`
			WaybillTotal int              `json:"waybill_total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("响应解不开：%v", err)
	}
	if n := len(out.Data.Waybills); n > driverTaskLimit {
		t.Errorf("返回了 %d 张运单，上限是 %d——手机上会拉一个几百 KB 到几 MB 的包", n, driverTaskLimit)
	}
	if out.Data.WaybillTotal != total {
		t.Errorf("waybill_total=%d，库里实际 %d：界面要靠这个数说「共 N 单，先显示 M 单」，"+
			"给错了就等于把一页说成了全量", out.Data.WaybillTotal, total)
	}
	if out.Data.WaybillTotal <= len(out.Data.Waybills) {
		t.Errorf("这个司机有 %d 张在途单却没被截断（返回 %d）——上限没生效",
			total, len(out.Data.Waybills))
	}
}
