-- 对账维度改为「项目 / 线路 + 账期」，而不是按合同。
--
-- 为什么不按合同：一份长期合同可能跑十年，底下是无数订单、运单与对账单，
-- 按合同出账等于把十年流水堆成一张单。合同的职责到「定价」为止（见 001），
-- 出账的归集维度应该是业务上真正会一起核对的那个单元：**项目**或**线路**，
-- 再叠加账期。故这里给对账单加 scope 三元组，并把 001 里加的 contract 列撤掉。

-- 业务项目：一个客户下的一个具体业务单元（某仓某年度配送、某工程专线…）
CREATE TABLE IF NOT EXISTS fin_project (
    id          uuid PRIMARY KEY,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    is_deleted  boolean     NOT NULL DEFAULT false,
    deleted_at  timestamptz,

    project_no  varchar(64)  NOT NULL,
    name        varchar(128) NOT NULL DEFAULT '',
    customer_id uuid,                              -- 业务归属客户
    contract_id uuid,                              -- 关联价格合同（可选：价仍从合同来）
    start_date  date,
    end_date    date,
    status      varchar(16)  NOT NULL DEFAULT 'active',   -- active/paused/closed
    manager_id  uuid,                              -- 项目负责人
    remark      varchar(255) NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS fin_project_no_key ON fin_project (project_no);
CREATE INDEX IF NOT EXISTS fin_project_customer_idx ON fin_project (customer_id, status);

-- 订单选项目，派车时运单继承；费用经运单归到项目
ALTER TABLE ops_order   ADD COLUMN IF NOT EXISTS project_id uuid;
ALTER TABLE ops_waybill ADD COLUMN IF NOT EXISTS project_id uuid;
CREATE INDEX IF NOT EXISTS ops_waybill_project_idx ON ops_waybill (project_id);

-- 对账单的归集范围：project / route / all（对手方本期全部）
ALTER TABLE fin_statement ADD COLUMN IF NOT EXISTS scope_type varchar(16)  NOT NULL DEFAULT 'all';
ALTER TABLE fin_statement ADD COLUMN IF NOT EXISTS scope_id   uuid;
ALTER TABLE fin_statement ADD COLUMN IF NOT EXISTS scope_name varchar(128) NOT NULL DEFAULT '';

-- 撤掉 001 里按合同出账的列：合同管价不管账
ALTER TABLE fin_statement DROP COLUMN IF EXISTS contract_id;
ALTER TABLE fin_statement DROP COLUMN IF EXISTS contract_no;

-- 一笔费用只能进一张对账单——数据库级保证，不靠应用层自觉。
-- 旧实现归集时不排除已入账记录，账期填重叠或重跑一次就会重复计费；
-- 应用层的 NOT EXISTS 只能挡住"顺序执行"的重复，挡不住并发两次生成。
DELETE FROM fin_statement_line a USING fin_statement_line b
 WHERE a.expense_record_id IS NOT NULL
   AND a.expense_record_id = b.expense_record_id
   AND a.ctid > b.ctid;
CREATE UNIQUE INDEX IF NOT EXISTS fin_statement_line_expense_uniq
    ON fin_statement_line (expense_record_id) WHERE expense_record_id IS NOT NULL;
