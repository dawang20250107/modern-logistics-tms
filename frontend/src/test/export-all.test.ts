import { describe, expect, it, vi } from "vitest";

// 导出必须导全，而不是"看起来像全"。
//
// 服务端分页的表格，导出如果照当前页来，用户点一下拿到的是一页——
// 表头齐、格式对、文件打得开，只是少了绝大部分。这套系统在调度台上
// 栽过同一条（把 8336 说成 20），所以这条单独钉。
//
// 这里模拟的是**真实服务端的行为**：page_size 有上限 200，请求 500 会被夹到 200。
// 第一版 fetchAll 的终止条件是「拿到的比要的少 = 最后一页」，
// 在这个夹取行为下第一页就停了——实测 8367 条只导出 200 条，
// 而按钮上什么都不说。用例里必须带上这个夹取，否则测不出那个错。

vi.mock("../api/client", () => ({
  apiGet: vi.fn(),
}));

const { apiGet } = await import("../api/client");
const { fetchAllPages, EXPORT_MAX } = await import("../api/useServerTable");

/** 造一个会把 page_size 夹到 200 的假服务端——真实服务端就是这么夹的 */
function fakeServer(total: number, cap = 200) {
  return (url: string) => {
    const q = new URLSearchParams(url.split("?")[1] ?? "");
    const page = Number(q.get("page") ?? 1);
    const size = Math.min(Number(q.get("page_size") ?? 20), cap);
    const start = (page - 1) * size;
    const items = Array.from({ length: Math.max(0, Math.min(size, total - start)) }, (_, i) => ({
      id: String(start + i),
    }));
    return Promise.resolve({ items, total, page, page_size: size, pages: Math.ceil(total / size) });
  };
}

const mockGet = apiGet as unknown as ReturnType<typeof vi.fn>;

describe("导出取全量", () => {
  it("服务端把 page_size 夹到 200 时仍然翻完所有页", async () => {
    mockGet.mockImplementation(fakeServer(8367));
    const rows = await fetchAllPages("/orders", "");
    expect(rows).toHaveLength(8367);
  });

  it("总数正好是一页时不多翻", async () => {
    mockGet.mockImplementation(fakeServer(200));
    mockGet.mockClear();
    const rows = await fetchAllPages("/orders", "");
    expect(rows).toHaveLength(200);
    expect(mockGet).toHaveBeenCalledTimes(1);
  });

  it("空结果不死循环", async () => {
    mockGet.mockImplementation(fakeServer(0));
    const rows = await fetchAllPages("/orders", "");
    expect(rows).toHaveLength(0);
  });

  it("带着筛选条件翻页（导出要跟着用户看到的那批走）", async () => {
    const seen: string[] = [];
    mockGet.mockImplementation((u: string) => {
      seen.push(u);
      return fakeServer(500)(u);
    });
    await fetchAllPages("/orders", "search=abc&filter=%7B%7D");
    expect(seen.length).toBeGreaterThan(1);
    for (const u of seen) {
      expect(u).toContain("search=abc");
      expect(u).toContain("filter=");
    }
  });

  it("上限之内截断，不会无限翻", async () => {
    mockGet.mockImplementation(fakeServer(EXPORT_MAX + 5000));
    const rows = await fetchAllPages("/orders", "", 1000);
    expect(rows).toHaveLength(1000);
  });
});
