package orders

import (
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

// scanOrder 把主查询一行映射为与 Django OrderSerializer 逐字段对齐的 JSON 对象。
// Decimal 一律以字符串输出（DRF DecimalField 语义），时间走 RFC3339。
func scanOrder(rows pgx.Rows, currentUserID string, isChief bool) (map[string]any, error) {
	var (
		id, orderNo, channel, source, sourceType, businessType, priority, settlementType, status string
		freightTerm, freightPayer, codStatus                                                     string
		customerID, codAmount, cargoWeightTon, cargoVolumeCbm, cargoValue, quotedAmount          *string
		customerName, customerLevel                                                              string
		contactName, contactPhone, origin, destination                                           string
		pickupAddress, pickupContactName, pickupContactPhone                                     string
		deliveryAddress, deliveryContactName, deliveryContactPhone                               string
		cargoDesc, packageType, temperatureRange                                                 string
		cargoQuantity                                                                            *int
		isHazardous                                                                              bool
		expectedPickupAt, expectedDeliveryAt, deliveredAt, claimedAt, pooledAt, assignedAt       *time.Time
		slaStatus                                                                                string
		claimedByID, assignedToID, createdByID                                                   *string
		claimedByName, assignedToName, assignedByName, createdByName                             string
		rawText, aiConversationID, remark                                                        string
		parseMeta                                                                                map[string]any
		createdAt                                                                                time.Time
		waybillNos                                                                               []string
		firstDispatched                                                                          *time.Time
		excOpenCount, excMaxLevel                                                                int
		approvalStatus, approvalRemark                                                           string
		approvedAt                                                                               *time.Time
		cargoItemsJSON, stopsJSON                                                                json.RawMessage
	)

	if err := rows.Scan(
		&id, &orderNo, &customerID, &customerName, &customerLevel,
		&channel, &source, &sourceType, &businessType, &priority, &settlementType, &status,
		&freightTerm, &freightPayer, &codAmount, &codStatus,
		&contactName, &contactPhone, &origin, &destination,
		&pickupAddress, &pickupContactName, &pickupContactPhone,
		&deliveryAddress, &deliveryContactName, &deliveryContactPhone,
		&cargoDesc, &cargoQuantity, &cargoWeightTon, &cargoVolumeCbm,
		&cargoValue, &packageType, &isHazardous, &temperatureRange, &quotedAmount,
		&expectedPickupAt, &expectedDeliveryAt, &slaStatus, &deliveredAt,
		&claimedByID, &claimedByName, &claimedAt, &pooledAt,
		&assignedToID, &assignedToName,
		&assignedByName, &assignedAt,
		&createdByID, &createdByName, &rawText, &aiConversationID,
		&parseMeta, &remark, &createdAt,
		&waybillNos, &firstDispatched,
		&excOpenCount, &excMaxLevel,
		&approvalStatus, &approvalRemark, &approvedAt,
		&cargoItemsJSON, &stopsJSON,
	); err != nil {
		return nil, err
	}

	// lock_state / dispatchable（与序列化器逻辑一致）
	lockState := "free"
	if claimedByID != nil {
		if *claimedByID == currentUserID {
			lockState = "mine"
		} else {
			lockState = "locked"
		}
	} else if assignedToID != nil {
		if *assignedToID == currentUserID {
			lockState = "assigned_mine"
		} else {
			lockState = "assigned_other"
		}
	}
	dispatchable := isChief ||
		(claimedByID != nil && *claimedByID == currentUserID) ||
		(assignedToID != nil && *assignedToID == currentUserID)

	excLevel := ""
	switch excMaxLevel {
	case 3:
		excLevel = "high"
	case 2:
		excLevel = "medium"
	case 1:
		excLevel = "low"
	}

	// 保留微秒（RFC3339Nano），与 Django isoformat 精度一致
	var dispatchedAt any
	if firstDispatched != nil {
		dispatchedAt = firstDispatched.Format(time.RFC3339Nano)
	}

	return map[string]any{
		"id": id, "order_no": orderNo, "customer": customerID,
		"customer_name": customerName, "customer_level": customerLevel,
		"channel": channel, "source": source, "source_type": sourceType,
		"business_type": businessType, "priority": priority, "settlement_type": settlementType,
		"status": status,
		"freight_term": freightTerm, "freight_term_label": freightTermLabel[freightTerm],
		"freight_payer": freightPayer, "freight_payer_label": freightPayerLabel[freightPayer],
		"cod_amount": codAmount, "cod_status": codStatus,
		"contact_name": contactName, "contact_phone": contactPhone,
		"origin": origin, "destination": destination,
		"pickup_address": pickupAddress, "pickup_contact_name": pickupContactName, "pickup_contact_phone": pickupContactPhone,
		"delivery_address": deliveryAddress, "delivery_contact_name": deliveryContactName, "delivery_contact_phone": deliveryContactPhone,
		"cargo_desc": cargoDesc, "cargo_quantity": cargoQuantity,
		"cargo_weight_ton": cargoWeightTon, "cargo_volume_cbm": cargoVolumeCbm,
		"cargo_value": cargoValue, "package_type": packageType,
		"is_hazardous": isHazardous, "temperature_range": temperatureRange, "quoted_amount": quotedAmount,
		"expected_pickup_at": expectedPickupAt, "expected_delivery_at": expectedDeliveryAt,
		"sla_status": slaStatus, "delivered_at": deliveredAt,
		"claimed_by": claimedByID, "claimed_by_name": claimedByName, "claimed_at": claimedAt, "pooled_at": pooledAt,
		"assigned_to": assignedToID, "assigned_to_name": assignedToName,
		"assigned_by_name": assignedByName, "assigned_at": assignedAt,
		"dispatchable": dispatchable, "lock_state": lockState, "dispatched_at": dispatchedAt,
		"exception_count": excOpenCount, "exception_level": excLevel,
		"created_by": createdByID, "created_by_name": createdByName,
		"raw_text": rawText, "ai_conversation_id": aiConversationID, "parse_meta": parseMeta,
		"remark": remark, "created_at": createdAt,
		"waybill_nos": waybillNos,
		"cargo_items": cargoItemsJSON, "stops": stopsJSON, "attachments": []any{},
		"approval_status": approvalStatus, "approval_remark": approvalRemark, "approved_at": approvedAt,
	}, nil
}
