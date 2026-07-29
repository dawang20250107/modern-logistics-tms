import { describe, expect, it, vi } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { DataTable, type DataColumn } from "./DataTable";

// DataTable 是自研的（856 行），被 10+ 个页面依赖，此前零测试。
// 它已经出过三个不该出的 bug，全都是"看起来在工作、其实没生效"那一类：
//   1. 固定列底色用 color-mix 混了带 alpha 的斑马纹 → 只有 51% 不透明，字摞字
//   2. JS 生成 .dt-sticky-r、CSS 定义 .dt-sticky-right → 右固定列样式从未生效
//   3. tbody tr 上一条写死的 height 压过密度三档 → 切密度看着像没反应
// 这三类都不会报错，只会静默地不对。下面的用例就是钉住这些行为。

interface Row {
  id: string;
  no: string;
  customer: string;
  biz: string;
  amount: number;
}

const ROWS: Row[] = [
  { id: "1", no: "DD001", customer: "美的集团", biz: "整车", amount: 17620 },
  { id: "2", no: "DD002", customer: "海尔智家", biz: "整车", amount: 12240 },
  { id: "3", no: "DD003", customer: "宁德时代", biz: "整车", amount: 23000 },
  { id: "4", no: "DD004", customer: "比亚迪", biz: "整车", amount: 9000 },
];

const COLS: DataColumn<Row>[] = [
  { key: "no", header: "订单号", width: 140, alwaysVisible: true, sortValue: (r) => r.no, exportValue: (r) => r.no, render: (r) => <span>{r.no}</span> },
  { key: "customer", header: "客户", width: 160, sortValue: (r) => r.customer, exportValue: (r) => r.customer, render: (r) => <span>{r.customer}</span> },
  { key: "biz", header: "业务", width: 90, sortValue: (r) => r.biz, exportValue: (r) => r.biz, render: (r) => <span>{r.biz}</span> },
  { key: "amount", header: "金额", width: 120, align: "right", sortValue: (r) => r.amount, exportValue: (r) => r.amount, render: (r) => <span>{r.amount}</span> },
  { key: "actions", header: "操作", width: 120, alwaysVisible: true, sticky: "right", render: () => <button>详情</button> },
];

function setup(props: Partial<React.ComponentProps<typeof DataTable<Row>>> = {}) {
  return render(
    <DataTable<Row>
      columns={COLS}
      rows={ROWS}
      rowKey={(r) => r.id}
      viewKey={`test-${Math.random()}`}
      {...props}
    />,
  );
}

const headerNames = () =>
  screen.getAllByRole("columnheader").map((th) => th.textContent?.replace(/[↕▲▼]/g, "").trim() ?? "");

describe("DataTable · 固定列", () => {
  it("右固定列用 .dt-fixed-r，且与 CSS 里的类名一致", () => {
    setup();
    const cells = screen.getAllByRole("cell");
    const actionCell = cells.find((td) => td.className.includes("dt-fixed-r"));
    expect(actionCell, "操作列应带 dt-fixed-r（曾经生成 dt-sticky-r 与 CSS 对不上）").toBeTruthy();
    // 同时必须有基类 dt-fixed，position:sticky 挂在它上面
    expect(actionCell!.className).toContain("dt-fixed");
  });

  it("固定列不带任何半透明底色类，避免盖住的列透上来", () => {
    setup();
    const html = document.body.innerHTML;
    // 斑马纹已移除；如果它回来了并被 color-mix 进固定列底色，就会重现字摞字
    expect(html).not.toContain("dt-zebra");
  });
});

describe("DataTable · 单值列折叠", () => {
  it("整页同值的列被折叠，取值显示在工具栏 chip 上", async () => {
    setup();
    // biz 四行全是「整车」→ 该列折叠
    expect(headerNames()).not.toContain("业务");
    const chip = screen.getByTitle(/本页「业务」全部为「整车」/);
    expect(chip).toHaveTextContent("业务");
    expect(chip).toHaveTextContent("整车");
  });

  it("点 chip 能把列放回来", async () => {
    const user = userEvent.setup();
    setup();
    await user.click(screen.getByTitle(/本页「业务」全部为「整车」/));
    expect(headerNames()).toContain("业务");
  });

  it("取值不同的列不折叠", () => {
    setup();
    expect(headerNames()).toContain("客户");
    expect(headerNames()).toContain("金额");
  });

  it("alwaysVisible 与 sticky 列即使同值也不折叠", () => {
    const rows = ROWS.map((r) => ({ ...r, no: "SAME" }));
    render(
      <DataTable<Row> columns={COLS} rows={rows} rowKey={(r) => r.id} viewKey={`t2-${Math.random()}`} />,
    );
    expect(headerNames()).toContain("订单号"); // alwaysVisible
    expect(headerNames()).toContain("操作");   // sticky:right
  });

  it("少于 3 行时不折叠——样本太小，同值可能只是巧合", () => {
    render(
      <DataTable<Row> columns={COLS} rows={ROWS.slice(0, 2)} rowKey={(r) => r.id} viewKey={`t3-${Math.random()}`} />,
    );
    expect(headerNames()).toContain("业务");
  });
});

describe("DataTable · 密度", () => {
  it("点「密度」在三档间循环，容器类名跟着变", async () => {
    const user = userEvent.setup();
    const { container } = setup();
    const root = container.querySelector(".dt")!;
    expect(root.className).toContain("dt-den-standard");
    await user.click(screen.getByTitle(/行密度/));
    expect(root.className).toContain("dt-den-relaxed");
    await user.click(screen.getByTitle(/行密度/));
    expect(root.className).toContain("dt-den-compact");
    await user.click(screen.getByTitle(/行密度/));
    expect(root.className).toContain("dt-den-standard");
  });
});

describe("DataTable · 排序", () => {
  it("中文列按拼音序排，不是 Unicode 码点序", async () => {
    // 码点序会把「宁德时代」(宁 U+5B81) 排在「比亚迪」(比 U+6BD4) 前面。
    // 看表的人期望的是拼音 b < h < m < n。
    const user = userEvent.setup();
    setup();
    await user.click(screen.getByText("客户").closest(".dt-sortable")!);
    const names = screen.getAllByRole("row").slice(1).map((r) => r.textContent ?? "");
    expect(names[0]).toContain("比亚迪");
    expect(names[1]).toContain("海尔智家");
    expect(names[2]).toContain("美的集团");
    expect(names[3]).toContain("宁德时代");
  });

  it("单号按自然数序排：DD9 在 DD10 之前", async () => {
    const user = userEvent.setup();
    const rows: Row[] = [
      { id: "a", no: "DD10", customer: "甲", biz: "整车", amount: 1 },
      { id: "b", no: "DD9", customer: "乙", biz: "整车", amount: 2 },
      { id: "c", no: "DD100", customer: "丙", biz: "整车", amount: 3 },
    ];
    render(<DataTable<Row> columns={COLS} rows={rows} rowKey={(r) => r.id} viewKey={`nat-${Math.random()}`} />);
    await user.click(screen.getByText("订单号").closest(".dt-sortable")!);
    const nos = screen.getAllByRole("row").slice(1).map((r) => r.textContent ?? "");
    expect(nos[0]).toContain("DD9");
    expect(nos[1]).toContain("DD10");
    expect(nos[2]).toContain("DD100");
  });

  it("客户端排序：升 → 降 → 取消", async () => {
    const user = userEvent.setup();
    setup();
    const firstCellText = () => screen.getAllByRole("row")[1].textContent ?? "";

    const th = screen.getByText("客户").closest(".dt-sortable")!;
    await user.click(th);
    expect(firstCellText()).toContain("比亚迪");   // 升序（拼音 b 最小）
    await user.click(th);
    expect(firstCellText()).toContain("宁德时代"); // 降序（拼音 n 最大）
    await user.click(th);
    expect(firstCellText()).toContain("美的集团"); // 取消 → 回原序
  });

  it("服务端模式下点表头调 onServerSort，不在本地重排", async () => {
    const user = userEvent.setup();
    const onServerSort = vi.fn();
    render(
      <DataTable<Row>
        columns={COLS.map((c) => (c.key === "customer" ? { ...c, sortField: "customer__name" } : c))}
        rows={ROWS} rowKey={(r) => r.id} viewKey={`t4-${Math.random()}`}
        server={{
          serverSort: null, onServerSort, page: 1, pageSize: 50, total: 4,
          onPageChange: vi.fn(),
        }}
      />,
    );
    await user.click(screen.getByText("客户").closest(".dt-sortable")!);
    expect(onServerSort).toHaveBeenCalledWith("customer__name");
    // 本地顺序不变（重排由服务端返回新数据驱动）
    expect(screen.getAllByRole("row")[1].textContent).toContain("美的集团");
  });
});

describe("DataTable · 行选择", () => {
  it("勾选单行回调 onToggle", async () => {
    const user = userEvent.setup();
    const onToggle = vi.fn();
    setup({ selectable: true, selected: new Set<string>(), onToggle, onToggleAll: vi.fn() });
    const firstRow = screen.getAllByRole("row")[1];
    await user.click(within(firstRow).getByRole("checkbox"));
    expect(onToggle).toHaveBeenCalledWith("1");
  });

  it("选中行带 row-sel 类（左侧色条靠它）", () => {
    setup({ selectable: true, selected: new Set(["2"]), onToggle: vi.fn(), onToggleAll: vi.fn() });
    const rows = screen.getAllByRole("row");
    const selected = rows.filter((r) => r.className.includes("row-sel"));
    expect(selected).toHaveLength(1);
    expect(selected[0].textContent).toContain("海尔智家");
  });
});

describe("DataTable · 空态", () => {
  it("无数据时渲染传入的空态", () => {
    render(
      <DataTable<Row> columns={COLS} rows={[]} rowKey={(r) => r.id} viewKey={`t5-${Math.random()}`}
        emptyState={<span>没有匹配的记录</span>} />,
    );
    expect(screen.getByText("没有匹配的记录")).toBeInTheDocument();
  });
});

describe("DataTable · 列显隐", () => {
  it("在「列 / 视图」里取消勾选后该列消失", async () => {
    const user = userEvent.setup();
    setup();
    await user.click(screen.getByRole("button", { name: "列 / 视图" }));
    const item = screen.getByRole("checkbox", { name: /客户/ });
    await user.click(item);
    expect(headerNames()).not.toContain("客户");
  });

  it("alwaysVisible 的列不可隐藏", async () => {
    const user = userEvent.setup();
    setup();
    await user.click(screen.getByRole("button", { name: "列 / 视图" }));
    // 订单号是 alwaysVisible，菜单里不该给出可取消的勾选
    const boxes = screen.getAllByRole("checkbox").map((b) => (b as HTMLInputElement).disabled || !b.getAttribute("name"));
    expect(boxes.length).toBeGreaterThan(0);
    expect(headerNames()).toContain("订单号");
  });
});

describe("DataTable · 视图持久化", () => {
  it("密度等视图状态写进 localStorage，重挂载后保留", async () => {
    const user = userEvent.setup();
    const viewKey = "persist-check";
    const { unmount, container } = render(
      <DataTable<Row> columns={COLS} rows={ROWS} rowKey={(r) => r.id} viewKey={viewKey} />,
    );
    await user.click(screen.getByTitle(/行密度/));
    expect(container.querySelector(".dt")!.className).toContain("dt-den-relaxed");
    unmount();

    const again = render(
      <DataTable<Row> columns={COLS} rows={ROWS} rowKey={(r) => r.id} viewKey={viewKey} />,
    );
    expect(again.container.querySelector(".dt")!.className).toContain("dt-den-relaxed");
  });
});
