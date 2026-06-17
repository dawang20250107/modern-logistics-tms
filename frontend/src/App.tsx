import { BrowserRouter, Route, Routes } from "react-router-dom";

import { AppLayout } from "./components/AppLayout";
import { ProtectedRoute } from "./components/ProtectedRoute";
import { AuthProvider } from "./auth/auth";
import { AiWorkbenchPage } from "./pages/AiWorkbenchPage";
import { ControlTowerPage } from "./pages/ControlTowerPage";
import { ExceptionsPage } from "./pages/ExceptionsPage";
import { LoginPage } from "./pages/LoginPage";
import { WaybillDetailPage } from "./pages/WaybillDetailPage";
import { WaybillsPage } from "./pages/WaybillsPage";

export function App() {
  return (
    <AuthProvider>
      <BrowserRouter>
        <Routes>
          <Route path="/login" element={<LoginPage />} />
          <Route element={<ProtectedRoute />}>
            <Route element={<AppLayout />}>
              <Route index element={<ControlTowerPage />} />
              <Route path="waybills" element={<WaybillsPage />} />
              <Route path="waybills/:no" element={<WaybillDetailPage />} />
              <Route path="exceptions" element={<ExceptionsPage />} />
              <Route path="ai" element={<AiWorkbenchPage />} />
            </Route>
          </Route>
        </Routes>
      </BrowserRouter>
    </AuthProvider>
  );
}
