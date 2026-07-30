import { Navigate, Outlet } from "react-router-dom";

import { useAuth } from "../auth/auth";
import { StateView } from "./StateView";

export function ProtectedRoute() {
  const { user, loading } = useAuth();
  if (loading) {
    return <div className="center-screen"><StateView kind="loading" /></div>;
  }
  if (!user) {
    return <Navigate to="/login" replace />;
  }
  return <Outlet />;
}
