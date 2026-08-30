// 带 JWT 与自动刷新的 API 客户端，统一解包 {success,data,error} 信封。

const API_BASE: string =
  (import.meta.env.VITE_API_BASE as string | undefined) ?? "http://127.0.0.1:8000/api/v1";

export const API_BASE_URL = API_BASE;

export interface Envelope<T> {
  success: boolean;
  data: T;
  error: null | { code: string; message: string };
}

export class ApiError extends Error {
  code: string;
  constructor(code: string, message: string) {
    super(message);
    this.code = code;
  }
}

/** 这个错是"没权限"而不是"出错了"。
 *
 * 后端两处闸的 code 拼写不一样（通用 CRUD 引擎回 permission_denied，
 * 各 handler 里回 PERMISSION_DENIED），所以这里不区分大小写。
 * 分辨这两者有实际意义：页面上写「数据获取出错，请重试」会让用户一直点重试，
 * 而真相是他的角色就是看不了这一块——重试一万次也一样。 */
export function isPermissionDenied(err: unknown): boolean {
  if (!(err instanceof ApiError)) return false;
  const c = err.code.toLowerCase();
  return c === "permission_denied" || c === "403";
}

let accessToken = localStorage.getItem("access") ?? "";
let refreshToken = localStorage.getItem("refresh") ?? "";

/** 退出登录时要把 refresh 交回服务端作废，所以需要读得到它。 */
export function getRefreshToken(): string {
  return refreshToken;
}

export function setTokens(access: string, refresh: string): void {
  accessToken = access;
  refreshToken = refresh;
  localStorage.setItem("access", access);
  localStorage.setItem("refresh", refresh);
}

export function clearTokens(): void {
  accessToken = "";
  refreshToken = "";
  localStorage.removeItem("access");
  localStorage.removeItem("refresh");
}

export function hasToken(): boolean {
  return Boolean(accessToken);
}

export function getAccess(): string {
  return accessToken;
}

async function tryRefresh(): Promise<boolean> {
  if (!refreshToken) return false;
  const resp = await fetch(`${API_BASE}/auth/token/refresh`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ refresh: refreshToken }),
  });
  if (!resp.ok) {
    clearTokens();
    return false;
  }
  const env = (await resp.json()) as Envelope<{ access: string; refresh?: string }>;
  if (env.success && env.data?.access) {
    accessToken = env.data.access;
    localStorage.setItem("access", accessToken);
    if (env.data.refresh) {
      refreshToken = env.data.refresh;
      localStorage.setItem("refresh", refreshToken);
    }
    return true;
  }
  clearTokens();
  return false;
}

async function raw(path: string, options: RequestInit, retry = true): Promise<Response> {
  const headers = new Headers(options.headers ?? {});
  // FormData 让浏览器自动设置 multipart 边界，不要手动设 Content-Type
  if (!headers.has("Content-Type") && typeof options.body === "string") {
    headers.set("Content-Type", "application/json");
  }
  if (accessToken) headers.set("Authorization", `Bearer ${accessToken}`);
  const resp = await fetch(`${API_BASE}${path}`, { ...options, headers });
  if (resp.status === 401 && retry && (await tryRefresh())) {
    return raw(path, options, false);
  }
  return resp;
}

export async function api<T>(path: string, options: RequestInit = {}): Promise<T> {
  const resp = await raw(path, options);
  if (resp.status === 204) return null as T; // No Content（如删除）
  const env = (await resp.json().catch(() => null)) as Envelope<T> | null;
  if (!env) throw new ApiError("BAD_JSON", "响应解析失败");
  if (!resp.ok || !env.success) {
    throw new ApiError(env.error?.code ?? String(resp.status), env.error?.message ?? "请求失败");
  }
  return env.data;
}

export const apiGet = <T>(path: string): Promise<T> => api<T>(path);
export const apiPost = <T>(path: string, body: unknown): Promise<T> =>
  api<T>(path, { method: "POST", body: JSON.stringify(body) });
export const apiPatch = <T>(path: string, body: unknown): Promise<T> =>
  api<T>(path, { method: "PATCH", body: JSON.stringify(body) });
export const apiDelete = <T>(path: string): Promise<T> =>
  api<T>(path, { method: "DELETE" });

// 下载（非 JSON，如 CSV 导出）：带鉴权 + 自动刷新，触发浏览器下载。
export async function apiDownload(path: string, filename: string): Promise<void> {
  const resp = await raw(path, {});
  if (!resp.ok) throw new ApiError(String(resp.status), "导出失败");
  const blob = await resp.blob();
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  a.click();
  URL.revokeObjectURL(url);
}
export const apiUpload = <T>(path: string, form: FormData): Promise<T> =>
  api<T>(path, { method: "POST", body: form });
