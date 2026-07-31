-- 对账单落归属组织，让数据范围（iam_role.data_scope）能作用到财务域。
--
-- 背景：订单、运单、异常、主数据的列表都会调 ScopeOrgIDs 收窄到「本组织及下级」，
-- 唯独财务域一次都没调过——因为 fin_statement 表根本没有组织列，无从收窄。
-- 于是一个只该看本网点的角色，打开对账中心看到的是全集团的应收敞口与逐客户账龄。
--
-- 为什么是新加一列，而不是在查询里 JOIN 回运单：
-- 对账单与运单是一对多，"这张单属于哪个组织"在 JOIN 里只能用聚合猜（max/any），
-- 跨组织的单会被随机归到某一个组织下——那不是收窄，那是错判。
-- 单独一列就能把这件事说清楚：**跨组织的对账单 organization_id 为 NULL**，
-- 而 NULL 在 scope 语义里是"只有 all 档看得见"，正是想要的保守答案。
--
-- 存量回填同理：只有当该单全部明细的运单同属一个组织时才回填，否则留 NULL。

ALTER TABLE fin_statement ADD COLUMN IF NOT EXISTS organization_id uuid;

CREATE INDEX IF NOT EXISTS fin_statement_organization_id_idx
    ON fin_statement (organization_id);

-- 回填：明细 → 运单 → 组织；组织唯一才回填，跨组织与无明细的留 NULL
-- （min(uuid) 在 PG 里不存在，先转 text 聚合再转回来；count(DISTINCT)=1 时取哪个都一样）
UPDATE fin_statement s
   SET organization_id = u.org_id,
       updated_at = now()
  FROM (
        SELECT l.statement_id,
               CASE WHEN count(DISTINCT w.organization_id) = 1
                    THEN min(w.organization_id::text)::uuid END AS org_id
          FROM fin_statement_line l
          JOIN ops_waybill w ON w.waybill_no = l.waybill_no
         WHERE w.organization_id IS NOT NULL
         GROUP BY l.statement_id
       ) u
 WHERE u.statement_id = s.id
   AND u.org_id IS NOT NULL
   AND s.organization_id IS DISTINCT FROM u.org_id;
