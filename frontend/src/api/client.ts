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

let accessToken = localStorage.getItem("access") ?? "";
let refreshToken = localStorage.getItem("refresh") ?? "";

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
