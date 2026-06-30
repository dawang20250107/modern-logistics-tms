import { BrowserRouter, Route, Routes } from "react-router-dom";

import { AppLayout } from "./components/AppLayout";
import { ProtectedRoute } from "./components/ProtectedRoute";
import { AuthProvider } from "./auth/auth";
import { AiWorkbenchPage } from "./pages/AiWorkbenchPage";
import { AlertsPage } from "./pages/AlertsPage";
import { AuditPage } from "./pages/AuditPage";
import { CommandCenterPage } from "./pages/CommandCenterPage";
import { CustomerOrderPage } from "./pages/CustomerOrderPage";
import { DriverPortalPage } from "./pages/DriverPortalPage";
import { ControlTowerPage } from "./pages/ControlTowerPage";
import { DashboardPage } from "./pages/DashboardPage";
import { DataCatalogPage } from "./pages/DataCatalogPage";
import { DispatchBoardPage } from "./pages/DispatchBoardPage";
import { ExceptionsPage } from "./pages/ExceptionsPage";
import { FleetPage } from "./pages/FleetPage";
import { LoginPage } from "./pages/LoginPage";
import { MonitorPage } from "./pages/MonitorPage";
import { OrderDetailPage } from "./pages/OrderDetailPage";
import { OrderIntakePage } from "./pages/OrderIntakePage";
import { PricingPage } from "./pages/PricingPage";
import { OrgCenterPage } from "./pages/OrgCenterPage";
import { ReconciliationPage } from "./pages/ReconciliationPage";
import { TrackingPage } from "./pages/TrackingPage";
import { WaybillDetailPage } from "./pages/WaybillDetailPage";
import { WaybillsPage } from "./pages/WaybillsPage";

export function App() {
  return (
    <AuthProvider>
      <BrowserRouter>
        <Routes>
          <Route path="/login" element={<LoginPage />} />
          <Route path="/track" element={<TrackingPage />} />
          <Route path="/submit" element={<CustomerOrderPage />} />
          <Route path="/driver" element={<DriverPortalPage />} />
          <Route element={<ProtectedRoute />}>
            <Route element={<AppLayout />}>
              <Route index element={<ControlTowerPage />} />
              <Route path="intake" element={<OrderIntakePage />} />
              <Route path="orders/:id" element={<OrderDetailPage />} />
              <Route path="dispatch-board" element={<DispatchBoardPage />} />
              <Route path="waybills" element={<WaybillsPage />} />
              <Route path="waybills/:no" element={<WaybillDetailPage />} />
              <Route path="command" element={<CommandCenterPage />} />
              <Route path="monitor" element={<MonitorPage />} />
              <Route path="fleet" element={<FleetPage />} />
              <Route path="alerts" element={<AlertsPage />} />
              <Route path="exceptions" element={<ExceptionsPage />} />
              <Route path="reconciliation" element={<ReconciliationPage />} />
              <Route path="pricing" element={<PricingPage />} />
              <Route path="dashboard" element={<DashboardPage />} />
              <Route path="catalog" element={<DataCatalogPage />} />
              <Route path="org" element={<OrgCenterPage />} />
              <Route path="audit" element={<AuditPage />} />
              <Route path="ai" element={<AiWorkbenchPage />} />
            </Route>
          </Route>
        </Routes>
      </BrowserRouter>
    </AuthProvider>
  );
}
