import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { apiDelete, apiGet, apiPatch, apiPost } from "../api/client";
import { confirmAction } from "../api/confirm";
import { EMPTY as DASH, fmtMoney, fmtNum } from "../api/format";
import { toast } from "../api/toast";
import type { Carrier, CostCatalog, Customer, Paginated, PricingRule } from "../api/types";
import { PRICE_TYPE_LABEL } from "../api/types";
import { StateView } from "../components/StateView";

const CHARGE_METHOD_LABEL: Record<string, string> = {
  tiered_weight: "按重量阶梯", flat: "整车一口价", per_volume: "按方计费",
  per_piece: "按件计费", per_km: "按公里计费", per_ton_km: "吨公里计费",
};

interface RuleForm {
  name: string;
  price_type: "income" | "cost";
  charge_method: string;
  expense_item_code: string;
  customer: string;
  carrier: string;
  route_name: string;
  base_price: string;
  unit_price: string;
  min_charge_qty: string;
  min_price: string;
  // 编辑期用字符串：数字类型下清空输入框会立刻变成 0，没法删了重填
  tier_prices: Array<{ min_ton: string; max_ton: string; price: string }>;
  volumetric_factor: string;
  fuel_surcharge_pct: string;
  priority: string;
  is_active: boolean;
}

// 方向决定默认科目：收入价默认运费收入，成本价默认运费。
// 原先两个方向共用一个写死的 "FREIGHT"，那个码在后端两份词表里都不存在。
const defaultItem = (t: string) => (t === "cost" ? "TRANSPORT_COST" : "TRANSPORT_INCOME");

const EMPTY: RuleForm = {
  name: "", price_type: "income", charge_method: "tiered_weight", expense_item_code: "TRANSPORT_INCOME", customer: "", carrier: "",
  route_name: "", base_price: "0", unit_price: "0", min_charge_qty: "0", min_price: "0",
  tier_prices: [], volumetric_factor: "0.33", fuel_surcharge_pct: "0", priority: "0", is_active: true,
};

export function PricingPage() {
  const queryClient = useQueryClient();
  const [typeFilter, setTypeFilter] = useState("");
  const [editing, setEditing] = useState<string | null>(null);
  const [form, setForm] = useState<RuleForm>(EMPTY);
  // 新增表单默认收起。新增一条计价规则是一个月一次的事，看规则列表是每天的事——
  // 实测这张常驻展开的表单吃掉本页 54% 的垂直空间，频率和空间分配完全反了。
  const [formOpen, setFormOpen] = useState(false);
  const set = <K extends keyof RuleForm>(k: K, v: RuleForm[K]) => setForm((f) => ({ ...f, [k]: v }));

  const rules = useQuery({
    queryKey: ["pricing-rules", typeFilter],
    queryFn: () => apiGet<Paginated<PricingRule>>(`/finance/pricing-rules?page_size=200${typeFilter ? `&price_type=${typeFilter}` : ""}`),
  });
  const customers = useQuery({ queryKey: ["customers"], queryFn: () => apiGet<Paginated<Customer>>("/customers?page_size=500") });
  // 费用科目词表。原先表单把 expense_item_code 写死成 "FREIGHT"——一个后端两份
  // 词表里都没有的码，于是规则算出来的钱落进一个谁也不认识的科目。
  // 现在后端加了枚举校验（会直接 400），这里必须给出真实可选项。
  const catalog = useQuery({ queryKey: ["cost-catalog"], queryFn: () => apiGet<CostCatalog>("/waybills/cost-catalog") });
  const carriers = useQuery({ queryKey: ["carriers"], queryFn: () => apiGet<Paginated<Carrier>>("/carriers?page_size=500") });
  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["pricing-rules"] });

  const payload = () => ({
    name: form.name, price_type: form.price_type, charge_method: form.charge_method,
    expense_item_code: form.expense_item_code || defaultItem(form.price_type),
    customer: form.customer || null, carrier: form.carrier || null, route_name: form.route_name,
    base_price: form.base_price || 0, unit_price: form.unit_price || 0,
    min_charge_qty: form.min_charge_qty || 0, min_price: form.min_price || 0,
    // 存进去必须是数字：算价那边 decFrom 只认 float64 / json.Number，
    // 收到字符串会取默认值 0，规则看着存上了、价却是零。
    tier_prices: form.tier_prices.map((t) => ({
      min_ton: Number(t.min_ton) || 0,
      max_ton: Number(t.max_ton) || 0,
      price: Number(t.price) || 0,
    })),
    volumetric_factor: form.volumetric_factor || 0.3333,
    fuel_surcharge_pct: form.fuel_surcharge_pct || 0,
    priority: Number(form.priority) || 0, is_active: form.is_active,
  });

  const addTier = () => setForm((f) => ({
    ...f,
    // 新一档的起点接着上一档的终点，省得每次都要回头看上一行
    tier_prices: [...f.tier_prices, {
      min_ton: f.tier_prices.length ? (f.tier_prices[f.tier_prices.length - 1].max_ton || "") : "0",
      max_ton: "", price: "",
    }],
  }));
  const setTier = (i: number, k: "min_ton" | "max_ton" | "price", v: string) =>
    setForm((f) => ({ ...f, tier_prices: f.tier_prices.map((t, j) => (j === i ? { ...t, [k]: v } : t)) }));
  const removeTier = (i: number) =>
    setForm((f) => ({ ...f, tier_prices: f.tier_prices.filter((_, j) => j !== i) }));

  // 阶梯价的校验。三条都是**会算错钱**的配置，不是格式挑剔：
  //   · 一档都没有 → 报价按 0 元/吨算，等于白配
  //   · 止 ≤ 起    → 这一档永远匹配不上，重量落进来会掉到下一档或算成 0
  //   · 有断档     → 落在缝里的重量匹配不到任何一档，同样算成 0
  // 匹配是「第一条命中的赢」，所以重叠不报错（边界值靠前的那档生效，
  // 说明文字里写了），但断档一定要拦。
  const tierProblem = (() => {
    if (form.charge_method !== "tiered_weight") return "";
    if (form.tier_prices.length === 0) return "按重量阶梯至少要配一档，否则报价会按 0 元/吨算。";
    const rows = form.tier_prices.map((t) => ({
      min: Number(t.min_ton), max: Number(t.max_ton), price: Number(t.price),
    }));
    for (let i = 0; i < rows.length; i++) {
      const r = rows[i];
      if (!Number.isFinite(r.min) || !Number.isFinite(r.max) || !Number.isFinite(r.price)) {
        return `第 ${i + 1} 档有空着或不是数字的格子。`;
      }
      if (r.max <= r.min) return `第 ${i + 1} 档的「止」要大于「起」，否则这一档永远匹配不上。`;
      if (r.price <= 0) return `第 ${i + 1} 档的单价要大于 0。`;
      if (i > 0 && r.min > rows[i - 1].max) {
        return `第 ${i} 档止于 ${rows[i - 1].max} 吨，第 ${i + 1} 档才从 ${r.min} 吨开始：` +
          `中间这段重量匹配不到任何一档，会按 0 元/吨算。`;
      }
    }
    return "";
  })();

  const reset = () => { setEditing(null); setForm(EMPTY); setFormOpen(false); };
  const save = useMutation({
    mutationFn: () => editing ? apiPatch(`/finance/pricing-rules/${editing}`, payload()) : apiPost("/finance/pricing-rules", payload()),
    onSuccess: () => { toast.success(editing ? "已更新合同价" : "已新增合同价"); reset(); invalidate(); },
  });
  const patch = useMutation({
    mutationFn: (v: { id: string; is_active: boolean }) => apiPatch(`/finance/pricing-rules/${v.id}`, { is_active: v.is_active }),
    onSuccess: (_d, v) => { invalidate(); toast.success(v.is_active ? "规则已启用" : "规则已停用"); },
    onError: (e: Error) => toast.error(e.message || "切换失败，请重试"),
  });
  const remove = useMutation({
    mutationFn: (id: string) => apiDelete(`/finance/pricing-rules/${id}`),
    onSuccess: () => { toast.success("已删除"); invalidate(); },
  });

  const startEdit = (r: PricingRule) => {
    setEditing(r.id);
    setFormOpen(true);
    setForm({
      name: r.name, price_type: r.price_type, charge_method: r.charge_method ?? "tiered_weight",
      expense_item_code: r.expense_item_code,
      customer: r.customer ?? "", carrier: r.carrier ?? "", route_name: r.route_name,
      base_price: r.base_price, unit_price: r.unit_price ?? "0", min_charge_qty: r.min_charge_qty ?? "0",
      min_price: r.min_price,
      tier_prices: (r.tier_prices ?? []).map((t) => ({
        min_ton: String(t.min_ton ?? ""), max_ton: String(t.max_ton ?? ""), price: String(t.price ?? ""),
      })),
      volumetric_factor: r.volumetric_factor, fuel_surcharge_pct: r.fuel_surcharge_pct,
      priority: String(r.priority), is_active: r.is_active,
    });
  };

  const items = rules.data?.items ?? [];

  return (
    <div className="stack">
      {/* 表单收起时整块不渲染。原先它留着一条 35px 的空面板头，
          上面只写着「合同价 / 计价规则」——顶栏标题已经写着"计价规则"，
          于是屏幕顶部连着两条各 35px 的横条，说的是同一件事。 */}
      {formOpen && (
      <div className="panel">
        <div className="panel-head">
          {editing ? "编辑合同价规则" : "新增合同价规则"}
          <div className="panel-actions">
            <button className="btn-ghost small" onClick={reset}>{editing ? "取消编辑" : "收起"}</button>
          </div>
        </div>
        <div className="form-section" style={{ borderBottom: "none" }}>
          <div className="grid-form">
            <label>规则名称 *<input value={form.name} onChange={(e) => set("name", e.target.value)} placeholder="如：比亚迪-沪蓉整车" /></label>
            <label>价格类型
              <select value={form.price_type} onChange={(e) => {
                const v = e.target.value as "income" | "cost";
                // 方向一换，原来那个科目多半属于另一个方向了，跟着切到该方向的默认科目。
                // 不切的话保存会被后端的枚举校验挡下，而用户看不出是哪一格的问题。
                setForm((f) => ({ ...f, price_type: v, expense_item_code: defaultItem(v) }));
              }}>
                {Object.entries(PRICE_TYPE_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
              </select>
            </label>
            <label>适用客户（空=通用）
              <select value={form.customer} onChange={(e) => set("customer", e.target.value)}>
                <option value="">全部客户</option>
                {(customers.data?.items ?? []).map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
              </select>
            </label>
            <label>适用承运商（空=通用）
              <select value={form.carrier} onChange={(e) => set("carrier", e.target.value)}>
                <option value="">全部承运商</option>
                {(carriers.data?.items ?? []).map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
              </select>
            </label>
            <label>适用线路（空=通用）<input value={form.route_name} onChange={(e) => set("route_name", e.target.value)} placeholder="上海→成都" /></label>
            <label>计费方式
              <select value={form.charge_method} onChange={(e) => set("charge_method", e.target.value)}>
                {Object.entries(CHARGE_METHOD_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
              </select>
            </label>
            <label>费用科目
              {/* 科目决定这笔钱记到哪一格。原先界面上够不着，全部落成写死的
                  "FREIGHT"——后端词表里没有这个码，财务看板按科目分组时它进一个
                  没有名字的桶，对账单行上显示的就是那个原始码。 */}
              <select value={form.expense_item_code} onChange={(e) => set("expense_item_code", e.target.value)}>
                {Object.entries(
                  form.price_type === "cost" ? (catalog.data?.cost_items ?? {}) : (catalog.data?.income_items ?? {}),
                ).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
              </select>
            </label>
            <label>{form.charge_method === "flat" ? "整车固定价(元)" : "起步价(元)"}<input value={form.base_price} onChange={(e) => set("base_price", e.target.value)} /></label>
            {form.charge_method !== "flat" && form.charge_method !== "tiered_weight" && (
              <label>单价({form.charge_method === "per_volume" ? "元/方" : form.charge_method === "per_piece" ? "元/件" : form.charge_method === "per_km" ? "元/公里" : "元/吨公里"})
                <input value={form.unit_price} onChange={(e) => set("unit_price", e.target.value)} />
              </label>
            )}
            {(form.charge_method === "per_volume" || form.charge_method === "per_piece" || form.charge_method === "per_ton_km" || form.charge_method === "tiered_weight") && (
              <label>最低计费量({form.charge_method === "per_volume" ? "方" : form.charge_method === "per_piece" ? "件" : "吨"})
                <input value={form.min_charge_qty} onChange={(e) => set("min_charge_qty", e.target.value)} />
              </label>
            )}
            <label>最低价(元下限)<input value={form.min_price} onChange={(e) => set("min_price", e.target.value)} /></label>
            {form.charge_method !== "flat" && form.charge_method !== "per_piece" && form.charge_method !== "per_km" && (
              // 体积折算系数决定"抛货按多少吨算"：1 方折 0.33 吨还是 0.25 吨，
              // 是跟客户一单一议的商务条件，不是常量。原先界面上碰不到，
              // 所有规则都是同一个 0.33。
              <label title="1 立方米折算成多少吨。轻抛货按折算重与实重取大者计费。">
                体积折算系数(吨/方)
                <input value={form.volumetric_factor} onChange={(e) => set("volumetric_factor", e.target.value)} />
              </label>
            )}
            <label>燃油附加率(如0.025)<input value={form.fuel_surcharge_pct} onChange={(e) => set("fuel_surcharge_pct", e.target.value)} /></label>
            <label>优先级（大者优先）<input value={form.priority} onChange={(e) => set("priority", e.target.value)} /></label>
            <label className="check-label"><input type="checkbox" checked={form.is_active} onChange={(e) => set("is_active", e.target.checked)} /> 启用</label>
          </div>

          {/* 阶梯价表。
              这块原先**整个不存在**：表单默认的计费方式就是「按重量阶梯」，
              而页面上没有任何地方能填那几档价。存下来的规则 tier_prices 是 []，
              算价时 pricePerTon 取 0，于是报价 = 起步价（起步价为 0 时报价就是 0）。
              用户在页面上认认真真配了一条合同价，报出来的是零——**没有报错**，
              只是数字不对，而看到数字的人默认它就是系统算出来的。
              和这一轮修的「承运合同」「司机报销」是同一种：功能写好了，界面上够不着。

              字段名必须是 min_ton / max_ton / price——算价那边就按这三个键取值
              （internal/finance/pricing.go 的 decFrom(tier["min_ton"]) …），
              改名字这里不会报错，只会静默算成 0。

              表格上那个 tier-table 类名是给走查脚本认的：这一页还有一张
              规则列表表格，只按 .dt-table 选会在将来某天悄悄选到另一张上去。 */}
          {form.charge_method === "tiered_weight" && (
            <div className="stack-sm" style={{ marginTop: 12 }}>
              <div className="cluster-between">
                <b style={{ fontSize: 13 }}>重量阶梯 *</b>
                <button className="btn-ghost small" onClick={addTier}>+ 加一档</button>
              </div>
              {form.tier_prices.length === 0 ? (
                <div className="muted small">
                  还没有任何一档。按重量阶梯必须至少配一档，否则报价会按 0 元/吨算。
                </div>
              ) : (
                <table className="dt-table tier-table">
                  <thead>
                    <tr>
                      <th style={{ width: "30%" }}>起（吨，含）</th>
                      <th style={{ width: "30%" }}>止（吨，含）</th>
                      <th style={{ width: "30%" }}>单价（元/吨）</th>
                      <th />
                    </tr>
                  </thead>
                  <tbody>
                    {form.tier_prices.map((t, i) => (
                      <tr key={i}>
                        <td><input className="cell-input" value={t.min_ton} inputMode="decimal"
                                   onChange={(e) => setTier(i, "min_ton", e.target.value)} /></td>
                        <td><input className="cell-input" value={t.max_ton} inputMode="decimal"
                                   onChange={(e) => setTier(i, "max_ton", e.target.value)} /></td>
                        <td><input className="cell-input" value={t.price} inputMode="decimal"
                                   onChange={(e) => setTier(i, "price", e.target.value)} /></td>
                        <td><button className="btn-ghost small" onClick={() => removeTier(i)}>删除</button></td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
              {tierProblem && <div className="form-error small">{tierProblem}</div>}
              <div className="muted small">
                取的是「总重落在哪一档，全部重量按那一档的单价算」，不是逐段累加。
                5 吨这种边界值两档都能匹配时，按列表里靠前的那一档——所以顺序有意义。
              </div>
            </div>
          )}

          <div className="muted small" style={{ marginTop: 8 }}>
            六种计费方式：整车一口价 / 按重量阶梯 / 按方 / 按件 / 按公里 / 吨公里；均取「最低价」为金额下限并叠加燃油附加。录单"自动报价"按客户/线路匹配优先级最高的收入价规则。
          </div>
        </div>
        <div className="form-actions">
          <button className="btn-primary" disabled={!form.name.trim() || !!tierProblem || save.isPending} onClick={() => save.mutate()}
                  title={!form.name.trim() ? "请先填写规则名称" : tierProblem || undefined}>
            {editing ? "保存修改" : "新增规则"}
          </button>
          <button className="btn-ghost" onClick={reset}>取消</button>
        </div>
      </div>
      )}

      <div className="panel">
        {/* 目录标题 / 方向筛选 / 新增，三件事原先占三条横条共 114px。
            筛选是这张表的一部分，不是另一个区块；新增是这张表的动作。合成一行。
            筛选也从圆角药丸改成全站统一的下划线页签语汇（.seg-tabs）——
            这三个按钮此前是整页唯一的圆角填充控件。 */}
        <div className="panel-head">
          合同价目录 · {rules.data?.total ?? 0}
          <div className="seg-tabs" style={{ marginRight: "auto", marginLeft: 10 }}>
            {([["", "全部"], ["income", "收入价"], ["cost", "支出价"]] as const).map(([k, label]) => (
              <button key={k} className={typeFilter === k ? "active" : ""} onClick={() => setTypeFilter(k)}>{label}</button>
            ))}
          </div>
          {!formOpen && (
            <div className="panel-actions">
              <button className="btn-ghost small" onClick={() => setFormOpen(true)}>+ 新增规则</button>
            </div>
          )}
        </div>
        {rules.isLoading ? (
          <StateView kind="loading" compact />
        ) : rules.isError ? (
          <StateView kind="error" hint="合同价目录暂时无法加载。" error={rules.error} onRetry={() => rules.refetch()} />
        ) : items.length === 0 ? (
          <StateView kind="empty" title="暂无合同价规则" hint="新增规则后，录单即可自动报价" />
        ) : (
          <div className="table-wrap"><table className="table pricing-table">
            <thead>
              <tr><th>合同规则名称</th><th>方向</th><th>计费方式</th><th>定向客户</th><th>定向承运商</th><th>线路路由</th><th>起步/固定价</th><th>阶梯价层数</th><th>重抛比</th><th>燃油金</th><th>启用</th><th>操作</th></tr>
            </thead>
            <tbody>
              {items.map((r) => (
                <tr key={r.id} style={editing === r.id ? { background: "var(--brand-light)" } : {}}>
                  {/* 这一行原来有五个彩色药丸：应收/应付、计费方式、阶梯层数、燃油率、启用。
                      能分类的东西全做成药丸，结果是一张玩具表。药丸宽度随文字变，
                      列内左对齐右不齐，扫列时必须逐个读字。
                      现在：方向用色点（应收绿/应付琥珀），其余一律纯文本。
                      「启用」列原来同时有勾选框和"启用"标签，两个都长得像能点——留勾选框。 */}
                  <td style={{ fontWeight: 600 }}>{r.name}</td>
                  <td><span className={`tag tag-dot tag-${r.price_type === "income" ? "low" : "medium"}`}>{r.price_type === "income" ? "应收" : "应付"}</span></td>
                  <td>{CHARGE_METHOD_LABEL[r.charge_method] ?? r.charge_method}</td>
                  <td>{r.customer_name || "全局通用"}</td>
                  <td>{r.carrier_name || "全局通用"}</td>
                  <td>{r.route_name || "全局通用"}</td>
                  <td className="mono num" style={{ fontWeight: 600, color: "var(--ink)" }}>{fmtMoney(r.base_price)}</td>
                  <td className="num">{r.tier_prices && r.tier_prices.length > 0 ? `${r.tier_prices.length} 级` : DASH}</td>
                  <td className="num">{r.volumetric_factor}</td>
                  <td className="num">{Number(r.fuel_surcharge_pct) > 0 ? <span style={{ color: "var(--amber)", fontWeight: 600 }}>+{fmtNum(Number(r.fuel_surcharge_pct) * 100, 1)}%</span> : DASH}</td>
                  <td>
                    <label className="switch-mini" title={r.is_active ? "已启用，点击停用" : "已停用，点击启用"}>
                      <input type="checkbox" checked={r.is_active} onChange={() => patch.mutate({ id: r.id, is_active: !r.is_active })} />
                      <span className={r.is_active ? undefined : "muted"}>{r.is_active ? "启用" : "停用"}</span>
                    </label>
                  </td>
                  <td className="row-actions">
                    <button className="btn-ghost" onClick={() => startEdit(r)}>编辑</button>
                    <button className="btn-ghost" disabled={remove.isPending} onClick={async () => {
                      if (await confirmAction({ message: `删除规则「${r.name}」？`, tone: "danger", confirmText: "删除" })) remove.mutate(r.id);
                    }}>删除</button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table></div>
        )}
      </div>
    </div>
  );
}
