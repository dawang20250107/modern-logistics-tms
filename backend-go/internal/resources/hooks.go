package resources

// 标准资源写路径上的少量副作用：对齐各 ViewSet 的 perform_create / 序列化器
// create() 覆写 / M2M 落库。都是「配置驱动之外」的那一点点定制。

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/masterdata"
)

// stampCreatedBy 对齐 perform_create(serializer.save(created_by=request.user))
func stampCreatedBy(_ context.Context, _ *masterdata.Handler, r *http.Request, body map[string]any) (map[string]any, error) {
	if uid := auth.UserID(r); uid != "" {
		body["created_by"] = uid
	}
	return body, nil
}

// stampUploadedBy 对齐 ReceiptViewSet.perform_create(uploaded_by=request.user)
func stampUploadedBy(_ context.Context, _ *masterdata.Handler, r *http.Request, body map[string]any) (map[string]any, error) {
	if uid := auth.UserID(r); uid != "" {
		body["uploaded_by"] = uid
	}
	return body, nil
}

// stampSentAt 补 DriverReminder.sent_at 的模型默认值 timezone.now
func stampSentAt(_ context.Context, _ *masterdata.Handler, _ *http.Request, body map[string]any) (map[string]any, error) {
	if v, ok := body["sent_at"]; !ok || v == nil || v == "" {
		body["sent_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	}
	return body, nil
}

// resolveWaybillNo 对齐 ExpenseRecordSerializer.create：外部系统按运单业务号推送，
// 内部也可直接传 waybill(UUID)；两者皆无则 400。
func resolveWaybillNo(ctx context.Context, h *masterdata.Handler, _ *http.Request, body map[string]any) (map[string]any, error) {
	no, _ := body["waybill_no"].(string)
	delete(body, "waybill_no") // write_only：不落库、不回显
	if wb, _ := body["waybill"].(string); wb == "" && no != "" {
		var id string
		err := h.DB.QueryRow(ctx, "SELECT id::text FROM ops_waybill WHERE waybill_no=$1", no).Scan(&id)
		if err != nil {
			return body, errors.New("运单不存在")
		}
		body["waybill"] = id
	}
	if wb, _ := body["waybill"].(string); wb == "" {
		return body, errors.New("需提供 waybill 或 waybill_no")
	}
	return body, nil
}

// setEmployeeGroupRoles 覆盖式写 EmployeeGroup.roles（ModelSerializer 的 M2M 语义：
// 请求里出现该键才改写，缺省则保持原样）
func setEmployeeGroupRoles(ctx context.Context, h *masterdata.Handler, id string, body map[string]any, _ bool) error {
	raw, has := body["roles"]
	if !has {
		return nil
	}
	list, _ := raw.([]any)
	if _, err := h.DB.Exec(ctx, "DELETE FROM iam_employee_group_roles WHERE employeegroup_id=$1::uuid", id); err != nil {
		return err
	}
	for _, it := range list {
		rid, _ := it.(string)
		if _, err := uuid.Parse(rid); err != nil {
			continue
		}
		if _, err := h.DB.Exec(ctx, `
			INSERT INTO iam_employee_group_roles (employeegroup_id, role_id)
			VALUES ($1::uuid, $2::uuid) ON CONFLICT DO NOTHING`, id, rid); err != nil {
			return err
		}
	}
	return nil
}

// kickReceiptOCR 对齐 perform_create 里的 process_receipt_ocr.delay：
// 异步执行，POST 响应仍是 ocr_status=pending（与 Celery 投递后立即返回一致）。
func kickReceiptOCR(_ context.Context, h *masterdata.Handler, id string, _ map[string]any, creating bool) error {
	if !creating {
		return nil
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		runReceiptOCR(ctx, h, id)
	}()
	return nil
}

// runReceiptOCR 复刻 apps/ops/tasks.process_receipt_ocr + apps/ops/ocr.run_ocr。
//
// 与证件 OCR 同款安全语义：未配置引擎时**绝不伪造**签收人/签收时间——伪造会污染
// e-POD 法律凭证并误导财务自动核销。因此无引擎时 status=manual、fields 为空，
// 且已有的人工/司机录入签收人永不被覆盖。
func runReceiptOCR(ctx context.Context, h *masterdata.Handler, id string) {
	if _, err := h.DB.Exec(ctx,
		"UPDATE ops_receipt SET ocr_status='processing', updated_at=now() WHERE id=$1::uuid", id); err != nil {
		return
	}
	var file, fileURL, signatory string
	if err := h.DB.QueryRow(ctx, `
		SELECT COALESCE(file,''), COALESCE(file_url,''), COALESCE(signatory,'')
		FROM ops_receipt WHERE id=$1::uuid`, id).Scan(&file, &fileURL, &signatory); err != nil {
		if _, err := h.DB.Exec(ctx, "UPDATE ops_receipt SET ocr_status='failed', updated_at=now() WHERE id=$1::uuid", id); err != nil {
			slog.Warn("回单识别状态写库失败", "err", err)
		}
		return
	}
	source := file
	if source == "" {
		source = fileURL
	}
	result := recognizeReceipt(source)
	if s, _ := result["fields"].(map[string]any)["signatory"].(string); s != "" && signatory == "" {
		signatory = s
	}
	rj, _ := json.Marshal(result)
	status, _ := result["status"].(string)
	if _, err := h.DB.Exec(ctx, `
		UPDATE ops_receipt SET ocr_result=$2::jsonb, signatory=$3, ocr_status=$4, updated_at=now()
		WHERE id=$1::uuid`, id, rj, signatory, status); err != nil {
		slog.Warn("回单识别状态写库失败", "err", err)
	}
}

func recognizeReceipt(source string) map[string]any {
	provider := ocrProvider()
	if provider == "" {
		return map[string]any{
			"provider": "none", "status": "manual", "source": source,
			"fields": map[string]any{},
			"note":   "未配置回单 OCR 引擎，签收信息待人工录入/核验。",
		}
	}
	return map[string]any{
		"provider": provider, "status": "manual", "source": source,
		"fields": map[string]any{},
		"note":   "OCR 引擎 " + provider + " 尚未接入实现，签收信息待人工录入。",
	}
}

// snapshotPartyName 合同落库后回填对手方名称快照。
// 快照而非 JOIN：合同是财务凭证，客户/承运商日后改名不应让历史合同跟着变。
func snapshotPartyName(ctx context.Context, h *masterdata.Handler, id string, body map[string]any, _ bool) error {
	if v, _ := body["party_name"].(string); v != "" {
		return nil // 显式给了就不覆盖
	}
	_, err := h.DB.Exec(ctx, `
		UPDATE fin_contract c SET party_name = COALESCE((
			SELECT name FROM md_customer WHERE id = c.party_id AND c.party_type='customer'
			UNION ALL
			SELECT name FROM md_carrier  WHERE id = c.party_id AND c.party_type='carrier'
			LIMIT 1), '')
		WHERE c.id = $1::uuid`, id)
	return err
}
