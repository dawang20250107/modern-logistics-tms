-- 运输合同（商务/价格合同）：一客一价、一商一价的载体。
--
-- 为什么要这层：原来计价规则漂在全局 fin_pricing_rule 上，靠
-- customer/carrier/route/vehicle_type 四个字段做匹配、按 priority 取第一条。
-- 后果是（1）答不出「这单是按哪份合同哪个价算的」；（2）改一条规则会影响历史与
-- 所有客户；（3）没有生效期，调价会把去年的单重算成今年的价。
-- 把价格挂到有起止期的合同上，这三件事一次解决。
--
-- 合同类型分四档，对应真实签约形态：
--   long_term  长期合同（年框，effective_to 可为空）
--   short_term 短期合同（有明确起止）
--   temporary  临时合同（单次/短期特价，优先级最高，压住长期价）
--   agreement  仅协议无合同（口头/邮件确认，仍要能配价并留痕）

CREATE TABLE IF NOT EXISTS fin_contract (
    id              uuid PRIMARY KEY,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    is_deleted      boolean     NOT NULL DEFAULT false,
    deleted_at      timestamptz,

    contract_no     varchar(64)  NOT NULL,
    name            varchar(128) NOT NULL DEFAULT '',
    contract_type   varchar(16)  NOT NULL DEFAULT 'long_term',
    -- 合同对手方：customer→应收侧价格，carrier→应付侧价格
    party_type      varchar(16)  NOT NULL,
    party_id        uuid         NOT NULL,
    party_name      varchar(128) NOT NULL DEFAULT '',

    effective_from  date         NOT NULL,
    effective_to    date,                       -- 长期合同可为空（无固定到期）
    signed_at       date,
    status          varchar(16)  NOT NULL DEFAULT 'active',

    -- 结算条款（缺省继承对手方档案，合同上可覆盖）
    settlement_type varchar(16)  NOT NULL DEFAULT 'monthly',
    credit_days     integer      NOT NULL DEFAULT 30,
    billing_day     integer      NOT NULL DEFAULT 1,

    file_url        varchar(200) NOT NULL DEFAULT '',
    remark          varchar(255) NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX IF NOT EXISTS fin_contract_no_key ON fin_contract (contract_no);
-- 找生效合同的主查询路径：对手方 + 状态 + 生效期
CREATE INDEX IF NOT EXISTS fin_contract_party_idx
    ON fin_contract (party_type, party_id, status, effective_from DESC);

-- 计价规则挂到合同下；contract_id 为空表示「无合同的全局兜底价」，
-- 供临时/仅协议场景使用，不破坏存量数据。
ALTER TABLE fin_pricing_rule ADD COLUMN IF NOT EXISTS contract_id uuid;
ALTER TABLE fin_pricing_rule ADD COLUMN IF NOT EXISTS effective_from date;
ALTER TABLE fin_pricing_rule ADD COLUMN IF NOT EXISTS effective_to date;
CREATE INDEX IF NOT EXISTS fin_pricing_rule_contract_idx ON fin_pricing_rule (contract_id);

-- 费用记录留合同快照：对账与审计时能直接答「按哪份合同算的」，
-- 合同后续改名/改号也不影响历史凭证。
ALTER TABLE fin_expense_record ADD COLUMN IF NOT EXISTS contract_id uuid;
ALTER TABLE fin_expense_record ADD COLUMN IF NOT EXISTS contract_no varchar(64) NOT NULL DEFAULT '';

-- 对账单也记合同（按合同出账时用；跨合同归集时留空）
ALTER TABLE fin_statement ADD COLUMN IF NOT EXISTS contract_id uuid;
ALTER TABLE fin_statement ADD COLUMN IF NOT EXISTS contract_no varchar(64) NOT NULL DEFAULT '';
