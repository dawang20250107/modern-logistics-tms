// Package contracts 承运合同：正文模板与生成。
//
// 单独成包是因为它有两个入口——派单时自动生成，以及运单详情页上按需生成
// （派单时没司机、后补司机的运单，原先永远拿不到合同）。合同是出事时
// 双方唯一的书面依据，两个入口必须出同一份东西。
package contracts

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// cstZone 合同签订日期按中国时区落，不按服务器时区。
var cstZone = time.FixedZone("CST", 8*3600)

// Input 生成一份合同所需的全部事实。
type Input struct {
	WaybillID    string
	WaybillNo    string
	DriverID     string
	DriverName   string
	VehiclePlate string
	TrailerID    *string
	Route        string
	CargoDesc    string
	Weight       decimal.Decimal
	Quantity     int
	Agreed       *decimal.Decimal
}

// nextNoScoped 按天递增的单号（与 orders 域同一张计数表、同一套规则）。
func nextNoScoped(ctx context.Context, tx pgx.Tx, prefix, scopeName string) (string, error) {
	day := time.Now().In(cstZone).Format("20060102")
	var v int
	err := tx.QueryRow(ctx, `
		INSERT INTO ops_number_counter (scope, value) VALUES ($1, 1)
		ON CONFLICT (scope) DO UPDATE SET value = ops_number_counter.value + 1
		RETURNING value`, scopeName+":"+day).Scan(&v)
	if err != nil {
		return "", err
	}
	return prefix + day + padSeq(v), nil
}

func padSeq(v int) string {
	s := ""
	for n := 100000; n >= 1; n /= 10 {
		s += string(rune('0' + (v/n)%10))
	}
	return s
}

// Generate 生成一份承运合同（文本；PDF 留空不阻断）。
//
// 两个调用方：派单事务里自动生成，以及运单详情页上的「生成合同」按需生成。
// 合同正文只在这里有一份——它是承运责任、运费金额、异常责任的书面依据，
// 两份模板各自演化的话，两个入口出来的合同条款会不一样，而没人会去比对。
func Generate(ctx context.Context, tx pgx.Tx, in Input) error {
	contractNo, err := nextNoScoped(ctx, tx, "HT", "contract")
	if err != nil {
		return err
	}
	waybillID, waybillNo := in.WaybillID, in.WaybillNo
	route, cargoDesc := in.Route, in.CargoDesc
	weight, quantity, agreed := in.Weight, in.Quantity, in.Agreed
	vehPlate, trailerPlate := "—", "—"
	if in.VehiclePlate != "" {
		vehPlate = in.VehiclePlate
	}
	if in.TrailerID != nil {
		_ = tx.QueryRow(ctx, `SELECT plate_no FROM md_vehicle WHERE id=$1::uuid`, *in.TrailerID).Scan(&trailerPlate)
	}
	if cargoDesc == "" {
		cargoDesc = "见运单"
	}
	// 约定运费 = 该运单应付合计（此刻即议定快照金额）
	freight := decimal.Zero
	if agreed != nil {
		freight = *agreed
	}
	drvPhone := ""
	_ = tx.QueryRow(ctx, `SELECT COALESCE(phone,'') FROM md_driver WHERE id=$1::uuid`, in.DriverID).Scan(&drvPhone)
	if drvPhone == "" {
		drvPhone = "—"
	}
	content := fmt.Sprintf(`运输承运合同

合同编号：%s
关联运单：%s
签订日期：%s

一、承运信息
  承运司机：%s    联系电话：%s
  牵引车牌：%s    挂车牌：%s

二、运输内容
  线路：%s
  货物：%s    重量：%s 吨    件数：%d 件

三、运费
  约定运费（应付）：人民币 %s 元

四、约定条款
  1. 承运方应按约定时间、线路完成提货与送货，确保货物安全。
  2. 全程接受平台 GPS 跟踪与节点回传，按要求上传回单。
  3. 异常情况应第一时间报备，责任与费用按平台规则处理。
  4. 本合同经司机线上确认即生效，与书面合同具有同等效力。

承运方（司机）确认：__________      平台（智运）：__________
`, contractNo, waybillNo, time.Now().In(cstZone).Format("2006-01-02"),
		in.DriverName, drvPhone, vehPlate, trailerPlate, route, cargoDesc, weight.String(), quantity, freight.String())
	cid, _ := uuid.NewV7()
	if _, err := tx.Exec(ctx, `
		INSERT INTO ops_contract (id, created_at, updated_at, contract_no, template_code, content,
		  driver_reply, confirm_status, pdf, driver_id, waybill_id)
		VALUES ($1, now(), now(), $2, '', $3, '', 'pending', '', $4::uuid, $5::uuid)`,
		cid.String(), contractNo, content, in.DriverID, waybillID); err != nil {
		return err
	}
	evID, _ := uuid.NewV7()
	pj, _ := json.Marshal(map[string]any{"contract_no": contractNo})
	_, err = tx.Exec(ctx, `
		INSERT INTO ops_waybill_event (id, created_at, updated_at, waybill_id, event_type, event_time, source, resource, payload)
		VALUES ($1, now(), now(), $2::uuid, 'contract_generated', now(), 'contract', $3, $4)`,
		evID.String(), waybillID, waybillNo, pj)
	return err
}
