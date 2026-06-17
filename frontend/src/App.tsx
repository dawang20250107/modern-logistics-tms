import { BrowserRouter, Route, Routes } from "react-router-dom";

import { AppLayout } from "./components/AppLayout";
import { ProtectedRoute } from "./components/ProtectedRoute";
import { AuthProvider } from "./auth/auth";
import { AiWorkbenchPage } from "./pages/AiWorkbenchPage";
import { AlertsPage } from "./pages/AlertsPage";
import { CommandCenterPage } from "./pages/CommandCenterPage";
import { ControlTowerPage } from "./pages/ControlTowerPage";
import { ExceptionsPage } from "./pages/ExceptionsPage";
import { LoginPage } from "./pages/LoginPage";
import { MonitorPage } from "./pages/MonitorPage";
import { OrderIntakePage } from "./pages/OrderIntakePage";
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
              <Route path="intake" element={<OrderIntakePage />} />
              <Route path="waybills" element={<WaybillsPage />} />
              <Route path="waybills/:no" element={<WaybillDetailPage />} />
              <Route path="command" element={<CommandCenterPage />} />
              <Route path="monitor" element={<MonitorPage />} />
              <Route path="alerts" element={<AlertsPage />} />
              <Route path="exceptions" element={<ExceptionsPage />} />
              <Route path="ai" element={<AiWorkbenchPage />} />
            </Route>
          </Route>
        </Routes>
      </BrowserRouter>
    </AuthProvider>
  );
}
