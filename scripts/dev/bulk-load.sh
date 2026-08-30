#!/usr/bin/env bash
# 造量：往库里灌 N 条订单 + 约 0.6N 条运单，用于压测和查询计划验证。
#
#   bash scripts/dev/bulk-load.sh 50000     # 灌 5 万单
#   bash scripts/dev/bulk-load.sh --clean   # 清掉所有灌进去的数据
#
# 为什么需要它：演示库只有十几条单，压出来的数字全是"内存里翻一遍"的速度，
# 跟线上没有任何关系。索引有没有用、分页深了会不会退化、
# COUNT(*) 会不会成为瓶颈——这些只有在有量的时候才看得见。
#
# 灌进去的行一律以 LT- 打头（订单号/运单号），清理时按前缀删，
# 不会碰到 seed 造的演示业务链。
set -euo pipefail
: "${PGHOST:=127.0.0.1}"; : "${PGUSER:=tms}"; : "${PGPASSWORD:=tms}"; : "${PGDATABASE:=tms}"
export PGHOST PGUSER PGPASSWORD

q() { psql -d "$PGDATABASE" -v ON_ERROR_STOP=1 -tAc "$1"; }

if [ "${1:-}" = "--clean" ]; then
  echo "清理压测数据…"
  q "DELETE FROM ops_waybill WHERE waybill_no LIKE 'LT-%'"
  q "DELETE FROM ops_order   WHERE order_no  LIKE 'LT-%'"
  psql -d "$PGDATABASE" -q -c "VACUUM ANALYZE ops_order" -c "VACUUM ANALYZE ops_waybill"
  echo "  剩余 订单 $(q 'SELECT count(*) FROM ops_order') / 运单 $(q 'SELECT count(*) FROM ops_waybill')"
  exit 0
fi

N="${1:-50000}"
echo "灌 $N 条订单…"

# 状态分布按真实业务形态给：绝大多数单最终会走完，在途的是少数。
# 均匀分布会让"待处理"列表跟"已完成"列表一样长，压出来的分页行为是假的。
q "
INSERT INTO ops_order (
  id, created_at, updated_at, order_no, source, status, remark, cargo_desc,
  cargo_quantity, cargo_volume_cbm, cargo_weight_ton, channel, contact_name,
  contact_phone, destination, origin, parse_meta, raw_text, business_type,
  cargo_value, delivery_address, delivery_contact_name, delivery_contact_phone,
  is_deleted, is_hazardous, package_type, pickup_address, pickup_contact_name,
  pickup_contact_phone, priority, quoted_amount, settlement_type, source_type,
  temperature_range, sla_status, approval_remark, approval_status,
  ai_conversation_id, cod_amount, cod_status, freight_payer, freight_term,
  customer_id, created_by_id, expected_pickup_at, expected_delivery_at,
  claimed_by_id, claimed_at
)
SELECT
  gen_random_uuid(),
  now() - (g % 540) * interval '1 day' - (g % 1440) * interval '1 minute',
  now() - (g % 540) * interval '1 day',
  'LT-' || lpad(g::text, 9, '0'),
  'cs',
  -- 状态。注意下面还要给 dispatching 配上锁定人：
  -- 「调度中」意味着有人锁了这一单，产品里 claim/release 永远成对写这两列。
  -- 造数时只写状态不写锁定人，会造出一个产品自己产生不出来的状态——
  -- 而这种单在调度台的「待分配」里看得见、点「锁定」却恒定 409
  -- 「订单已被锁定或不在池中」（而其实没人锁）。
  -- 用 bulk-load 评估产品的人会以为一半的池子是坏的。
  (ARRAY['completed','completed','completed','completed','completed','completed',
         'confirmed','converted','dispatching','pooled','draft','pending_confirm'])[1 + g % 12],
  '', '普货 ' || (ARRAY['家电','汽配','化工原料','建材','纺织品','食品'])[1 + g % 6],
  1 + g % 40, (5 + g % 60)::numeric, (2 + g % 28)::numeric, 'cs',
  '联系人' || (g % 500), '138' || lpad((g % 100000000)::text, 8, '0'),
  (ARRAY['上海','杭州','宁波','苏州','南京','合肥','南昌','武汉'])[1 + (g / 7) % 8],
  (ARRAY['上海','杭州','宁波','苏州','无锡','常州','嘉兴','绍兴'])[1 + g % 8],
  '{}'::jsonb, '', 'ftl', (2000 + g % 90000)::numeric,
  '收货地址 ' || g, '收货人' || (g % 300), '139' || lpad((g % 100000000)::text, 8, '0'),
  false, (g % 97 = 0),
  (ARRAY['托盘','木箱','纸箱','裸装'])[1 + g % 4],
  '发货地址 ' || g, '发货人' || (g % 300), '137' || lpad((g % 100000000)::text, 8, '0'),
  -- 优先级词表是 {normal, urgent, vip}，不是想当然的 high/medium/low。
  -- 造量数据用错词表的后果不是"数据不好看"，是走查时会走出一堆假 bug——
  -- 上一轮就因为 seed 用了不存在的 source_type，差点去"修"一个本来正确的 UI。
  (ARRAY['normal','normal','normal','normal','normal','normal','normal','urgent','vip'])[1 + g % 9],
  (800 + g % 12000)::numeric, 'monthly',
  (ARRAY['enterprise','enterprise','enterprise','individual'])[1 + g % 4],
  '', 'pending', '', 'none', '', 0, 'none', 'shipper', 'prepaid',
  (SELECT id FROM md_customer OFFSET (g % GREATEST((SELECT count(*) FROM md_customer),1)) LIMIT 1),
  (SELECT id FROM accounts_user OFFSET (g % GREATEST((SELECT count(*) FROM accounts_user),1)) LIMIT 1),
  now() - (g % 540) * interval '1 day' + interval '6 hours',
  now() - (g % 540) * interval '1 day' + interval '2 days',
  -- dispatching = 有人锁了这一单，两列必须成对。其余状态一律为空。
  CASE WHEN (ARRAY['completed','completed','completed','completed','completed','completed',
                   'confirmed','converted','dispatching','pooled','draft','pending_confirm'])[1 + g % 12] = 'dispatching'
       THEN (SELECT id FROM accounts_user OFFSET (g % GREATEST((SELECT count(*) FROM accounts_user),1)) LIMIT 1)
       ELSE NULL END,
  CASE WHEN (ARRAY['completed','completed','completed','completed','completed','completed',
                   'confirmed','converted','dispatching','pooled','draft','pending_confirm'])[1 + g % 12] = 'dispatching'
       THEN now() - (g % 540) * interval '1 day' + interval '7 hours'
       ELSE NULL END
FROM generate_series(1, $N) g
ON CONFLICT (order_no) DO NOTHING
" >/dev/null

echo "按已完成/在途的订单派生运单…"
q "
INSERT INTO ops_waybill (
  id, created_at, updated_at, waybill_no, route_name, origin, destination,
  status, dispatch_status, risk_level, receipt_status, eta_drift_minutes,
  cargo_quantity, cargo_weight_ton, cargo_volume_cbm, dispatch_type,
  ai_conversation_id, cod_amount, cod_status, freight_payer, freight_term,
  platform_name, platform_order_no, order_id, customer_id, carrier_id,
  vehicle_id, driver_id, organization_id, planned_arrival, estimated_arrival
)
SELECT
  gen_random_uuid(), n.created_at + interval '2 hours', n.updated_at,
  'LT-' || substr(n.order_no, 4),
  n.origin || ' → ' || n.destination, n.origin, n.destination,
  (ARRAY['signed','signed','signed','signed','signed','signed',
         'arrived','in_transit','departed','loaded','dispatched'])[1 + (n.i % 11)::int],
  'assigned', (ARRAY['low','low','low','low','low','medium','high'])[1 + (n.i % 7)::int],
  (ARRAY['pending','returned','returned'])[1 + (n.i % 3)::int],
  ((n.i % 240) - 60)::int, n.cargo_quantity, n.cargo_weight_ton, n.cargo_volume_cbm,
  'outsource', '', 0, 'none', 'shipper', 'prepaid', '', '',
  n.id, n.customer_id,
  (SELECT id FROM md_carrier OFFSET (n.i % GREATEST((SELECT count(*) FROM md_carrier),1)) LIMIT 1),
  (SELECT id FROM md_vehicle OFFSET (n.i % GREATEST((SELECT count(*) FROM md_vehicle),1)) LIMIT 1),
  (SELECT id FROM md_driver  OFFSET (n.i % GREATEST((SELECT count(*) FROM md_driver),1))  LIMIT 1),
  (SELECT id FROM iam_organization OFFSET (n.i % GREATEST((SELECT count(*) FROM iam_organization),1)) LIMIT 1),
  n.expected_delivery_at, n.expected_delivery_at + (n.i % 180) * interval '1 minute'
FROM (SELECT o.*, row_number() OVER (ORDER BY o.order_no) AS i FROM ops_order o
      WHERE o.order_no LIKE 'LT-%' AND o.status IN ('completed','converted','dispatching')) AS n
ON CONFLICT (waybill_no) DO NOTHING
" >/dev/null

# VACUUM 不能省，而且比 ANALYZE 更容易漏。
#   · ANALYZE 更新统计信息——不做，规划器按空表估行数，选出来的计划是错的。
#   · VACUUM 建可见性图（visibility map）——不做，index-only scan 用不了，
#     每次 count(*) 都要回表。实测：不 VACUUM 时 count(*) 走 Seq Scan 16.4ms，
#     VACUUM 之后同一句走 Index Only Scan 4.7ms，快 3 倍多。
# 生产上 autovacuum 会自己做这件事，所以这纯粹是"造完量立刻压测"的坑：
# 不补这一下，会把压测工具自己的产物当成产品的性能问题去查。
echo "VACUUM ANALYZE（统计信息 + 可见性图，两个都要）…"
psql -d "$PGDATABASE" -q -c "VACUUM ANALYZE ops_order" -c "VACUUM ANALYZE ops_waybill"

echo "  订单 $(q 'SELECT count(*) FROM ops_order')  运单 $(q 'SELECT count(*) FROM ops_waybill')"
echo "  ops_order   $(q "SELECT pg_size_pretty(pg_total_relation_size('ops_order'))")"
echo "  ops_waybill $(q "SELECT pg_size_pretty(pg_total_relation_size('ops_waybill'))")"
