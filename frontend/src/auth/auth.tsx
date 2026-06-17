import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from "react";

import { apiGet, apiPost, clearTokens, hasToken, setTokens } from "../api/client";
import type { CurrentUser } from "../api/types";

interface AuthState {
  user: CurrentUser | null;
  loading: boolean;
  login: (username: string, password: string) => Promise<void>;
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

  function logout(): void {
    clearTokens();
    setUser(null);
  }

  const value = useMemo<AuthState>(() => ({ user, loading, login, logout }), [user, loading]);
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used within AuthProvider");
  return ctx;
}
