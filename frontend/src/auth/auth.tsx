import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from "react";

import { apiGet, apiPatch, apiPost, clearTokens, hasToken, setTokens } from "../api/client";
import type { CurrentUser } from "../api/types";

export interface RegisterInput {
  username: string;
  nickname?: string;
  phone?: string;
  password: string;
}
export interface ProfileInput {
  nickname?: string;
  phone?: string;
  email?: string;
}

interface AuthState {
  user: CurrentUser | null;
  loading: boolean;
  login: (username: string, password: string) => Promise<void>;
  register: (input: RegisterInput) => Promise<void>;
  updateProfile: (input: ProfileInput) => Promise<void>;
  changePassword: (oldPassword: string, newPassword: string) => Promise<void>;
  refreshMe: () => Promise<void>;
  logout: () => void;
}

const AuthContext = createContext<AuthState | undefined>(undefined);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<CurrentUser | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let active = true;
    async function bootstrap() {
      if (!hasToken()) {
        setLoading(false);
        return;
      }
      try {
        const me = await apiGet<CurrentUser>("/auth/me");
        if (active) setUser(me);
      } catch {
        clearTokens();
      } finally {
        if (active) setLoading(false);
      }
    }
    void bootstrap();
    return () => {
      active = false;
    };
  }, []);

  async function login(username: string, password: string): Promise<void> {
    const tokens = await apiPost<{ access: string; refresh: string }>("/auth/token", {
      username,
      password,
    });
    setTokens(tokens.access, tokens.refresh);
    const me = await apiGet<CurrentUser>("/auth/me");
    setUser(me);
  }

  async function register(input: RegisterInput): Promise<void> {
    const tokens = await apiPost<{ access: string; refresh: string }>("/auth/register", input);
    setTokens(tokens.access, tokens.refresh);
    const me = await apiGet<CurrentUser>("/auth/me");
    setUser(me);
  }

  async function updateProfile(input: ProfileInput): Promise<void> {
    const me = await apiPatch<CurrentUser>("/auth/me", input);
    setUser(me);
  }

  async function changePassword(oldPassword: string, newPassword: string): Promise<void> {
    await apiPost("/auth/change-password", { old_password: oldPassword, new_password: newPassword });
  }

  async function refreshMe(): Promise<void> {
    const me = await apiGet<CurrentUser>("/auth/me");
    setUser(me);
  }

  function logout(): void {
    clearTokens();
    setUser(null);
  }

  const value = useMemo<AuthState>(
    () => ({ user, loading, login, register, updateProfile, changePassword, refreshMe, logout }),
    [user, loading],
  );
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used within AuthProvider");
  return ctx;
}

/** 前端权限判定：超管（["*"]）或持有该权限点即可。与后端 effective_permissions 一致。 */
export function hasPerm(user: CurrentUser | null, code: string): boolean {
  if (!user) return false;
  if (user.is_superuser) return true;
  const perms = user.permissions ?? [];
  return perms.includes("*") || perms.includes(code);
}
