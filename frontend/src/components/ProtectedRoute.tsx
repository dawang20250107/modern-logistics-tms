import { Navigate, Outlet } from "react-router-dom";

import { useAuth } from "../auth/auth";

export function ProtectedRoute() {
  const { user, loading } = useAuth();
  if (loading) {
    return <div className="center-screen">加载中…</div>;
  }
  if (!user) {
    return <Navigate to="/login" replace />;
  }
  return <Outlet />;
}
