import { useQuery } from "@tanstack/react-query";

import { apiGet } from "./client";
import type { AuthMethods } from "./types";

/** 登录/注册页共用：服务端说了算，前端别把入口写死。
 *
 * 自助注册默认关闭，登录页要据此隐藏「注册新账号」——
 * 留一个点进去必然 403 的入口，比没有入口更糟。
 * 拿不到结果时按「关闭」处理：宁可少给一个入口，也不要给一个坏的。
 */
export function useAuthMethods() {
  const q = useQuery({
    queryKey: ["auth-methods"],
    queryFn: () => apiGet<AuthMethods>("/auth/methods"),
    staleTime: 5 * 60 * 1000,
    retry: 1,
  });
  return { registrationEnabled: q.data?.registration?.enabled ?? false, data: q.data };
}
