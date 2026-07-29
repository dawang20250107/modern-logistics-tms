package masterdata

// 司机/车辆证件 OCR（可插拔），对齐 apps/masterdata/credential_ocr.py。
//
// 关键安全语义原样保留：未配置引擎时**绝不伪造**证件号与有效期——伪造未来到期日
// 会把过期证件"洗"成永久有效，直接架空派车时的证件到期硬阻断。因此：
//   - 未配引擎 → status=manual、fields 全空、note 说明待人工录入
//   - 有效期永不自动回填，仅存 ocr_result 供人工核验后手动确认
//   - 已有人工录入的字段不被覆盖

import (
	"context"
	"encoding/json"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
)

func emptyOCRFields() map[string]any {
	return map[string]any{
		"name": "", "cert_no": "", "plate_no": "", "id_no": "", "expiry_date": nil,
	}
}

// recognizeCredential 识别证件；未接入具体 provider 时一律 status=manual，不造数
func recognizeCredential(source, credType string) map[string]any {
	provider := os.Getenv("OCR_PROVIDER")
	if provider == "" {
		return map[string]any{
			"provider": "none", "status": "manual", "source": source,
			"cred_type": credType, "fields": emptyOCRFields(),
			"note": "未配置证件 OCR 引擎，证件信息待人工录入/核验。",
		}
	}
	return map[string]any{
		"provider": provider, "status": "manual", "source": source,
		"cred_type": credType, "fields": emptyOCRFields(),
		"note": "OCR 引擎 " + provider + " 尚未接入实现，证件信息待人工录入。",
	}
}

// applyOCR 执行识别并谨慎回填（仅补空字段，有效期不自动写入）
func (h *Handler) applyOCR(ctx context.Context, credID string) error {
	var file, fileURL, credType, holder, certNo string
	if err := h.DB.QueryRow(ctx, `
		SELECT COALESCE(file,''), COALESCE(file_url,''), cred_type,
		       COALESCE(holder_name,''), COALESCE(cert_no,'')
		FROM md_driver_credential WHERE id=$1::uuid`, credID).
		Scan(&file, &fileURL, &credType, &holder, &certNo); err != nil {
		return err
	}
	source := file
	if source == "" {
		source = fileURL
	}
	result := recognizeCredential(source, credType)
	fields, _ := result["fields"].(map[string]any)

	// 仅在原字段为空时用 OCR 建议值填充（不覆盖人工录入）
	if holder == "" {
		if v, _ := fields["name"].(string); v != "" {
			holder = v
		} else if v, _ := fields["plate_no"].(string); v != "" {
			holder = v
		}
	}
	if certNo == "" {
		if v, _ := fields["cert_no"].(string); v != "" {
			certNo = v
		}
	}
	rj, _ := json.Marshal(result)
	status, _ := result["status"].(string)
	_, err := h.DB.Exec(ctx, `
		UPDATE md_driver_credential
		SET ocr_result=$2::jsonb, ocr_status=$3, holder_name=$4, cert_no=$5, updated_at=now()
		WHERE id=$1::uuid`, credID, rj, status, holder, certNo)
	return err
}

// CredentialAfterWrite 创建证件后触发 OCR 建档（对齐 perform_create）
func CredentialAfterWrite(ctx context.Context, h *Handler, id string, _ map[string]any, creating bool) error {
	if !creating {
		return nil
	}
	return h.applyOCR(ctx, id)
}

// CredentialOCR POST /driver-credentials/{id}/ocr —— 重新触发识别
func (h *Handler) CredentialOCR(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Err(w, http.StatusNotFound, "error", "No DriverCredential matches the given query.")
		return
	}
	if err := h.applyOCR(r.Context(), id); err != nil {
		httpx.Err(w, http.StatusNotFound, "error", "No DriverCredential matches the given query.")
		return
	}
	it, err := h.OneDetail(r.Context(), DriverCredsCfg, "dc.id = $1::uuid", id)
	if err != nil || it == nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "回读失败")
		return
	}
	httpx.JSON(w, http.StatusOK, it)
}
