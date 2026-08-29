package orders

// 单单派车：POST /orders/{id}/dispatch —— 对齐 apps/ops/order_dispatch.dispatch_order。
// 校验链：派单类型要素 → 订单状态/锁定/归属 → 承运商风控 → 车辆/司机行锁占用
// → 核载/车厢结构 → 证件/资质合规 → 转运单 + 承运状态 + 应付快照 + 双事件 + 合同。
// dispatch-suggestion / ymm-quote / dispatch-plan 属 AI 建议域，仍由代理提供。

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/auth"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/contracts"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/httpx"
	"github.com/dawang20250107/modern-logistics-tms/backend-go/internal/waybills"
)

var validDispatchTypes = map[string]bool{"own_vehicle": true, "fleet": true, "third_party": true, "platform": true}

type vehRow struct {
	ID, PlateNo, BodyType, Class string
	LoadTon, VolumeCbm           decimal.Decimal
	InspExp, InsurExp, MaintDue  *time.Time
}

type drvRow struct {
	ID, Name, LicenseType, QualCertNo string
	LicenseExp, QualExp               *time.Time
}

func lockVehicle(ctx context.Context, tx pgx.Tx, id string) (*vehRow, error) {
	v := &vehRow{}
	err := tx.QueryRow(ctx, `
		SELECT id::text, plate_no, body_type, vehicle_class, COALESCE(load_capacity_ton,0), COALESCE(volume_capacity_cbm,0),
		       inspection_expiry, insurance_expiry, maintenance_due_date
		FROM md_vehicle WHERE id=$1::uuid AND NOT is_deleted FOR UPDATE`, id,
	).Scan(&v.ID, &v.PlateNo, &v.BodyType, &v.Class, &v.LoadTon, &v.VolumeCbm, &v.InspExp, &v.InsurExp, &v.MaintDue)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return v, err
}

func lockDriver(ctx context.Context, tx pgx.Tx, id string) (*drvRow, error) {
	d := &drvRow{}
	err := tx.QueryRow(ctx, `
		SELECT id::text, name, COALESCE(license_type,''), COALESCE(qualification_cert_no,''),
		       license_expiry, qualification_expiry
		FROM md_driver WHERE id=$1::uuid AND NOT is_deleted FOR UPDATE`, id,
	).Scan(&d.ID, &d.Name, &d.LicenseType, &d.QualCertNo, &d.LicenseExp, &d.QualExp)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return d, err
}

func cstToday() time.Time {
	t, _ := time.Parse("2006-01-02", time.Now().In(cstZone).Format("2006-01-02"))
	return t
}

func expired(exp *time.Time, today time.Time) bool {
	return exp != nil && exp.Before(today)
}

// Dispatch POST /api/v1/orders/{id}/dispatch
func (h *Handler) Dispatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	me, err := h.Svc.UserByID(ctx, auth.UserID(r))
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "TOKEN_INVALID", "用户不存在")
		return
	}
	orderID := chi.URLParam(r, "id")
	if _, err := uuid.Parse(orderID); err != nil {
		httpx.Err(w, http.StatusNotFound, "error", "No Order matches the given query.")
		return
	}
	var body struct {
		DispatchType    string   `json:"dispatch_type"`
		Carrier         string   `json:"carrier"`
		Vehicle         string   `json:"vehicle"`
		Driver          string   `json:"driver"`
		Trailer         string   `json:"trailer"`
		CoDrivers       []string `json:"co_drivers"`
		PlatformName    string   `json:"platform_name"`
		PlatformOrderNo string   `json:"platform_order_no"`
		AgreedPayable   any      `json:"agreed_payable_amount"`
		PriceSource     string   `json:"price_source"`
		QuoteID         string   `json:"quote_id"`
		PriceRemark     string   `json:"price_remark"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	body.PlatformName = strings.TrimSpace(body.PlatformName)
	body.PlatformOrderNo = strings.TrimSpace(body.PlatformOrderNo)

	if !validDispatchTypes[body.DispatchType] {
		httpx.Err(w, http.StatusBadRequest, "INVALID_DISPATCH_TYPE", "派单类型非法。")
		return
	}
	// 要素校验（与 _assert_dispatch_requirements 同序：先于状态校验之后调用，
	// Django 中其实在状态/锁定/归属之后 —— 保持一致放到下方）
	var agreed *decimal.Decimal
	switch v := body.AgreedPayable.(type) {
	case float64:
		d := decimal.NewFromFloat(v)
		agreed = &d
	case string:
		if strings.TrimSpace(v) != "" {
			if d, err := decimal.NewFromString(v); err == nil {
				agreed = &d
			}
		}
	}

	tx, err := h.DB.Begin(ctx)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "事务开启失败")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	o := dispatchableOrder{}
	var isHaz bool
	var tempRange, bizType, cargoDesc string
	err = tx.QueryRow(ctx, `
		SELECT o.id::text, o.order_no, o.status, COALESCE(c.name,''), o.origin, o.destination, o.ai_conversation_id,
		       o.customer_id::text, o.claimed_by_id::text, o.assigned_to_id::text,
		       o.cargo_weight_ton, o.cargo_quantity, o.cargo_volume_cbm, o.cod_amount,
		       o.freight_term, o.freight_payer, o.expected_delivery_at,
		       o.is_hazardous, COALESCE(o.temperature_range,''), o.business_type, COALESCE(o.cargo_desc,''),
		       o.project_id::text
		FROM ops_order o LEFT JOIN md_customer c ON c.id=o.customer_id
		WHERE o.id=$1::uuid AND NOT o.is_deleted FOR UPDATE OF o`, orderID,
	).Scan(&o.ID, &o.OrderNo, &o.Status, &o.CustomerName, &o.Origin, &o.Destination, &o.AIConvID,
		&o.CustomerID, &o.ClaimedBy, &o.AssignedTo,
		&o.Weight, &o.Quantity, &o.VolumeCbm, &o.CodAmount,
		&o.FreightTerm, &o.FreightPayer, &o.ExpectedDeliveryAt,
		&isHaz, &tempRange, &bizType, &cargoDesc, &o.ProjectID)
	if err == pgx.ErrNoRows {
		httpx.Err(w, http.StatusNotFound, "error", "No Order matches the given query.")
		return
	}
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取订单失败")
		return
	}
	if o.Status != "pooled" && o.Status != "dispatching" && o.Status != "confirmed" {
		httpx.Err(w, http.StatusConflict, "ORDER_NOT_DISPATCHABLE", "订单当前状态不可派单。")
		return
	}
	if o.ClaimedBy == nil && o.AssignedTo == nil {
		httpx.Err(w, http.StatusConflict, "ORDER_NOT_LOCKED", "该订单尚在待分配池：请先锁定，或由总调度分派后再派单。")
		return
	}
	isChief, _ := h.isChiefDispatcher(ctx, me)
	if !isChief {
		mine := (o.ClaimedBy != nil && *o.ClaimedBy == me.ID) || (o.AssignedTo != nil && *o.AssignedTo == me.ID)
		if !mine {
			httpx.Err(w, http.StatusForbidden, "ORDER_NOT_YOURS", "该订单未分派/锁定给你：请由总调度分单，或先自行锁定后再调派。")
			return
		}
	}
	// 派单类型必填要素
	if body.DispatchType == "third_party" && body.Carrier == "" {
		httpx.Err(w, http.StatusBadRequest, "CARRIER_REQUIRED", "外包承运商派单必须选择承运商。")
		return
	}
	if body.DispatchType == "platform" && body.PlatformName == "" {
		httpx.Err(w, http.StatusBadRequest, "PLATFORM_REQUIRED", "网货平台派单必须填写平台名称。")
		return
	}
	if (body.DispatchType == "own_vehicle" || body.DispatchType == "fleet") && body.Vehicle == "" {
		httpx.Err(w, http.StatusBadRequest, "VEHICLE_REQUIRED", "自营派单必须选择车辆。")
		return
	}

	// 承运商风控（黑名单/停用/资质过期）
	var carrierID *string
	carrierName := ""
	if body.Carrier != "" {
		if _, err := uuid.Parse(body.Carrier); err == nil {
			name, reason, found := h.carrierBlockReason(ctx, body.Carrier)
			if found {
				carrierName = name
				carrierID = &body.Carrier
				if reason != "" {
					httpx.Err(w, http.StatusConflict, "CARRIER_NOT_ALLOWED", reason+"，不可派单。")
					return
				}
			}
		}
	}

	// 车辆/司机行锁 + 占用 + 适配 + 合规
	today := cstToday()
	var veh *vehRow
	if body.Vehicle != "" {
		if _, err := uuid.Parse(body.Vehicle); err == nil {
			if veh, err = lockVehicle(ctx, tx, body.Vehicle); err != nil {
				httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取车辆失败")
				return
			}
		}
	}
	var drv *drvRow
	if body.Driver != "" {
		if _, err := uuid.Parse(body.Driver); err == nil {
			if drv, err = lockDriver(ctx, tx, body.Driver); err != nil {
				httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "读取司机失败")
				return
			}
		}
	}
	const busySQL = `SELECT EXISTS(SELECT 1 FROM ops_waybill WHERE %s=$1::uuid
		AND status IN ('dispatched','loaded','departed','in_transit','pending_dispatch'))`
	if veh != nil {
		var busy bool
		_ = tx.QueryRow(ctx, fmt.Sprintf(busySQL, "vehicle_id"), veh.ID).Scan(&busy)
		if busy {
			httpx.Err(w, http.StatusConflict, "VEHICLE_BUSY", "车辆 "+veh.PlateNo+" 已被占用，不可重复派单。")
			return
		}
	}
	if drv != nil {
		var busy bool
		_ = tx.QueryRow(ctx, fmt.Sprintf(busySQL, "driver_id"), drv.ID).Scan(&busy)
		if busy {
			httpx.Err(w, http.StatusConflict, "DRIVER_BUSY", "司机 "+drv.Name+" 已被占用，不可重复派单。")
			return
		}
	}
	needsReefer := tempRange != "" || bizType == "coldchain"
	if veh != nil {
		// 车厢结构
		if needsReefer && veh.BodyType != "reefer" {
			httpx.Err(w, http.StatusConflict, "VEHICLE_BODY_MISMATCH", "车辆 "+veh.PlateNo+" 车厢结构不符：冷链货需冷藏车。")
			return
		}
		if isHaz && veh.BodyType != "hazmat" && veh.BodyType != "tank" {
			httpx.Err(w, http.StatusConflict, "VEHICLE_BODY_MISMATCH", "车辆 "+veh.PlateNo+" 车厢结构不符：危险品需危运/罐式车。")
			return
		}
		// 核载/容积
		capT, capV := veh.LoadTon, veh.VolumeCbm
		if (capT.GreaterThan(decimal.Zero) && o.Weight.GreaterThan(capT)) ||
			(capV.GreaterThan(decimal.Zero) && o.VolumeCbm.GreaterThan(capV)) {
			httpx.Err(w, http.StatusConflict, "VEHICLE_OVERLOADED", "车辆 "+veh.PlateNo+" 核载/容积不足以承运该订单货量，请改派更大车型。")
			return
		}
		// 证件合规
		issues := []string{}
		for _, p := range []struct {
			exp   *time.Time
			label string
		}{{veh.InspExp, "年检"}, {veh.InsurExp, "保险"}, {veh.MaintDue, "维保"}} {
			if expired(p.exp, today) {
				issues = append(issues, p.label)
			}
		}
		if len(issues) > 0 {
			httpx.Err(w, http.StatusConflict, "VEHICLE_NON_COMPLIANT", "车辆 "+veh.PlateNo+" 证件过期（"+strings.Join(issues, "/")+"），不可派车。")
			return
		}
	}
	if drv != nil {
		issues := []string{}
		lt := strings.ToUpper(drv.LicenseType)
		if veh != nil && (veh.Class == "tractor" || veh.Class == "trailer") && !strings.Contains(lt, "A2") {
			issues = append(issues, "准驾不足(牵引挂车需A2)")
		}
		if expired(drv.LicenseExp, today) {
			issues = append(issues, "驾照过期")
		}
		if isHaz {
			if drv.QualCertNo == "" {
				issues = append(issues, "缺危运从业资格")
			} else if expired(drv.QualExp, today) {
				issues = append(issues, "危运从业资格过期")
			}
		}
		if len(issues) > 0 {
			httpx.Err(w, http.StatusConflict, "DRIVER_NON_QUALIFIED", "司机 "+drv.Name+" 资质不符（"+strings.Join(issues, "/")+"），不可派单。")
			return
		}
	}

	// ── 转运单（convert_order_to_waybill）──
	if o.Status == "converted" || o.Status == "completed" {
		httpx.Err(w, http.StatusConflict, "ORDER_ALREADY_CONVERTED", "订单已派单/完成。")
		return
	}
	if o.Status == "cancelled" {
		httpx.Err(w, http.StatusConflict, "ORDER_CANCELLED", "订单已取消。")
		return
	}
	wbNo, err := nextNoScoped(ctx, tx, "YD", "waybill")
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "取号失败")
		return
	}
	wid, _ := uuid.NewV7()
	codStatus := "none"
	if o.CodAmount.GreaterThan(decimal.Zero) {
		codStatus = "pending"
	}
	dispatchStatus := dispatchStatusFor(body.DispatchType, drv != nil)
	var vehArg, drvArg, trailerArg *string
	if veh != nil {
		vehArg = &veh.ID
	}
	if drv != nil {
		drvArg = &drv.ID
	}
	if body.Trailer != "" {
		if _, err := uuid.Parse(body.Trailer); err == nil {
			var exists bool
			_ = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM md_vehicle WHERE id=$1::uuid AND NOT is_deleted)`, body.Trailer).Scan(&exists)
			if exists {
				trailerArg = &body.Trailer
			}
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO ops_waybill (id, created_at, updated_at, waybill_no, dispatch_type, platform_name, platform_order_no,
		  order_id, customer_id, carrier_id, vehicle_id, driver_id, trailer_id, route_name, ai_conversation_id, origin, destination,
		  status, dispatch_status, risk_level, receipt_status, eta_drift_minutes,
		  cargo_quantity, cargo_weight_ton, cargo_volume_cbm,
		  freight_term, freight_payer, cod_amount, cod_status, planned_arrival, project_id)
		VALUES ($1, now(), now(), $2, $3, $4, $5, $6::uuid, $7::uuid, $8::uuid, $9::uuid, $10::uuid, $11::uuid,
		  $12, $13, $14, $15, 'pending_dispatch', $16, 'none', 'not_due', 0, $17, $18, $19, $20, $21, $22, $23, $24, $25::uuid)`,
		wid.String(), wbNo, body.DispatchType, body.PlatformName, body.PlatformOrderNo,
		o.ID, o.CustomerID, carrierID, vehArg, drvArg, trailerArg,
		o.Origin+"→"+o.Destination, o.AIConvID, o.Origin, o.Destination,
		dispatchStatus, o.Quantity, o.Weight, o.VolumeCbm,
		o.FreightTerm, o.FreightPayer, o.CodAmount, codStatus, o.ExpectedDeliveryAt, o.ProjectID); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "运单写入失败")
		return
	}
	// 点位拷贝
	if _, err := tx.Exec(ctx, `
		INSERT INTO ops_waybill_stop (id, created_at, updated_at, waybill_id, seq, stop_type, city, address,
		       contact_name, contact_phone, lat, lng, radius_m, planned_eta, arrival_source, status, note)
		SELECT gen_random_uuid(), now(), now(), $2::uuid, seq, stop_type, city, address,
		       contact_name, contact_phone, NULL, NULL, 0, COALESCE(expected_end, expected_start), '', 'pending', cargo_note
		FROM ops_order_stop WHERE order_id=$1::uuid ORDER BY seq`, o.ID, wid.String()); err != nil {
		slog.Error("派单点位拷贝失败", "order", o.OrderNo, "waybill_id", wid.String(), "err", err)
	}
	// 司机分配：主驾 + 同行司机
	if drv != nil {
		mid, _ := uuid.NewV7()
		if _, err := tx.Exec(ctx, `INSERT INTO ops_waybill_driver (id, created_at, updated_at, waybill_id, driver_id, role, note)
			VALUES ($1, now(), now(), $2::uuid, $3::uuid, 'main', '')`, mid.String(), wid.String(), drv.ID); err != nil {
			slog.Error("主驾分配失败", "waybill_id", wid.String(), "driver_id", drv.ID, "err", err)
		}
	}
	for _, co := range body.CoDrivers {
		if co == "" || (drv != nil && co == drv.ID) {
			continue
		}
		if _, err := uuid.Parse(co); err != nil {
			continue
		}
		cid, _ := uuid.NewV7()
		if _, err := tx.Exec(ctx, `INSERT INTO ops_waybill_driver (id, created_at, updated_at, waybill_id, driver_id, role, note)
			SELECT $1, now(), now(), $2::uuid, d.id, 'co', '' FROM md_driver d
			WHERE d.id=$3::uuid AND NOT d.is_deleted
			  AND NOT EXISTS (SELECT 1 FROM ops_waybill_driver x WHERE x.waybill_id=$2::uuid AND x.driver_id=d.id)`,
			cid.String(), wid.String(), co); err != nil {
			slog.Error("同行司机分配失败", "waybill_id", wid.String(), "driver_id", co, "err", err)
		}
	}
	// 订单回写已派单
	if _, err := tx.Exec(ctx, `UPDATE ops_order SET status='converted', updated_at=now() WHERE id=$1::uuid`, o.ID); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "订单回写失败")
		return
	}
	// 议定应付快照
	priceSource := strings.TrimSpace(body.PriceSource)
	if agreed != nil && agreed.GreaterThan(decimal.Zero) {
		payeeType, payeeRef := "other", ""
		switch {
		case body.DispatchType == "platform":
			payeeType = "platform"
			payeeRef = body.PlatformName
			if payeeRef == "" {
				payeeRef = "网货平台"
			}
		case carrierID != nil:
			payeeType, payeeRef = "carrier", carrierName
		case drv != nil:
			payeeType, payeeRef = "driver", drv.Name
		}
		ps := priceSource
		if ps == "" {
			ps = "manual"
		}
		snapIn, _ := json.Marshal(map[string]any{
			"weight_ton": o.Weight.InexactFloat64(), "volume_cbm": o.VolumeCbm.InexactFloat64(),
			"quantity": o.Quantity, "route": o.Origin + "→" + o.Destination,
		})
		snapCalc, _ := json.Marshal(map[string]any{"agreed_payable": agreed.InexactFloat64(), "note": "派单议定应付金额快照"})
		eid, _ := uuid.NewV7()
		if _, err := tx.Exec(ctx, `
			INSERT INTO fin_expense_record (id, created_at, updated_at, waybill_id, direction, expense_item_code,
			  amount, currency, occurred_at, risk_status, source_system, external_id, payee_type, payee_ref,
			  remark, price_source, quote_id, pricing_rule_id, pricing_rule_name, charge_method, matched_condition,
			  input_snapshot, calculation_detail, rule_snapshot)
			VALUES ($1, now(), now(), $2::uuid, 'payable', 'TRANSPORT_COST', $3, 'CNY', now(), 'normal', '', '',
			  $4, $5, $6, $7, $8, '', '', '', '', $9, $10, '{}'::jsonb)`,
			eid.String(), wid.String(), *agreed, payeeType, payeeRef,
			strings.TrimSpace(body.PriceRemark), ps, strings.TrimSpace(body.QuoteID), snapIn, snapCalc); err != nil {
			httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "费用快照失败")
			return
		}
	}
	// 事件：订单 + 运单
	_ = txEvent(ctx, tx, o.ID, "dispatched", "", "converted", me.ID, "dispatch",
		map[string]any{"waybill_no": wbNo, "dispatch_type": body.DispatchType})
	res := ""
	switch {
	case carrierName != "":
		res = carrierName
	case body.PlatformName != "":
		res = body.PlatformName
	case veh != nil:
		res = veh.PlateNo
	}
	agreedF := 0.0
	if agreed != nil {
		agreedF = agreed.InexactFloat64()
	}
	evPriceSource := priceSource
	if evPriceSource == "" && agreed != nil && agreed.GreaterThan(decimal.Zero) {
		evPriceSource = "manual"
	}
	wp, _ := json.Marshal(map[string]any{
		"dispatch_type": body.DispatchType, "dispatch_status": dispatchStatus,
		"price_source": evPriceSource, "agreed_payable": agreedF, "quote_id": body.QuoteID,
	})
	wevID, _ := uuid.NewV7()
	if _, err := tx.Exec(ctx, `
		INSERT INTO ops_waybill_event (id, created_at, updated_at, waybill_id, event_type, event_time, source, resource, payload)
		VALUES ($1, now(), now(), $2::uuid, 'dispatched', now(), 'dispatch', $3, $4)`,
		wevID.String(), wid.String(), res, wp); err != nil {
		slog.Error("派单事件落库失败", "waybill_id", wid.String(), "err", err)
	}
	// 工作流编排：带司机派单自动生成承运合同（文本；PDF 生成留 Django/收官期）
	// 正文模板在 internal/contracts 里，与运单详情页的「按需生成」共用同一份。
	if drv != nil {
		plate := ""
		if veh != nil {
			plate = veh.PlateNo
		}
		if err := contracts.Generate(ctx, tx, contracts.Input{
			WaybillID: wid.String(), WaybillNo: wbNo,
			DriverID: drv.ID, DriverName: drv.Name,
			VehiclePlate: plate, TrailerID: trailerArg,
			Route: o.Origin + "→" + o.Destination, CargoDesc: cargoDesc,
			Weight: o.Weight, Quantity: o.Quantity, Agreed: agreed,
		}); err != nil {
			httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "合同生成失败")
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "提交失败")
		return
	}
	it, err := waybills.SerializeByNo(ctx, h.DB, wbNo)
	if err != nil || it == nil {
		httpx.Err(w, http.StatusInternalServerError, "INTERNAL", "回读失败")
		return
	}
	httpx.JSON(w, http.StatusCreated, it)
}

// dispatchStatusFor 对齐 _dispatch_status_for
func dispatchStatusFor(dispatchType string, hasDriver bool) string {
	switch dispatchType {
	case "third_party":
		if hasDriver {
			return "accepted"
		}
		return "pending_accept"
	case "platform":
		return "pending_accept"
	}
	if hasDriver {
		return "driver_assigned"
	}
	return "pending_driver_submit"
}
