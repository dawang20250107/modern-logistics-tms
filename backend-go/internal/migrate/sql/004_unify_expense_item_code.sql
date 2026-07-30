-- 统一运费科目编码：'freight' → 'TRANSPORT_COST'
--
-- 派单与批派一直写小写的 'freight'，而费用科目目录（cost_items）里只有大写的
-- 'TRANSPORT_COST'。后果有两个，都不是"算错钱"，但都在误导人：
--   1. 成本构成图把运费裂成两条——一条叫「运费」、一条叫未翻译的「freight」，
--      看图的人会以为这是两种不同的成本。
--   2. 批次详情里按 expense_item_code='freight' 取分摊应付，于是凡是由计价引擎
--      生成（写 TRANSPORT_COST）的运单，那一栏是空的。
--
-- 这是 Django 时代就存在的不一致（order_dispatch.py 写 "freight"，
-- cost_items.py 只登记 TRANSPORT_COST），移植时被原样带了过来。写入端已统一，
-- 这里把存量归并，让历史数据和新数据落在同一个科目上。

UPDATE fin_expense_record
   SET expense_item_code = 'TRANSPORT_COST', updated_at = now()
 WHERE expense_item_code = 'freight';
