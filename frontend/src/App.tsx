import { BrowserRouter, Route, Routes } from "react-router-dom";

import { AppLayout } from "./components/AppLayout";
import { ProtectedRoute } from "./components/ProtectedRoute";
import { AuthProvider } from "./auth/auth";
import { AdminHubPage } from "./pages/AdminHubPage";
import { AuditPage } from "./pages/AuditPage";
import { CustomerOrderPage } from "./pages/CustomerOrderPage";
import { DriverPortalPage } from "./pages/DriverPortalPage";
import { ControlTowerPage } from "./pages/ControlTowerPage";
import { DispatchBoardPage } from "./pages/DispatchBoardPage";
import { ForgotPasswordPage } from "./pages/ForgotPasswordPage";
import { FleetPage } from "./pages/FleetPage";
import { LoginPage } from "./pages/LoginPage";
import { OrderDetailPage } from "./pages/OrderDetailPage";
import { OrderIntakePage } from "./pages/OrderIntakePage";
import { OrderManagePage } from "./pages/OrderManagePage";
import { PricingPage } from "./pages/PricingPage";
import { OrgCenterPage } from "./pages/OrgCenterPage";
import { ProfilePage } from "./pages/ProfilePage";
import { ReconciliationPage } from "./pages/ReconciliationPage";
import { RegisterPage } from "./pages/RegisterPage";
import { TrackingPage } from "./pages/TrackingPage";
import { WaybillDetailPage } from "./pages/WaybillDetailPage";

export function App() {
  return (
    <AuthProvider>
      <BrowserRouter>
        <Routes>
          <Route path="/login" element={<LoginPage />} />
          <Route path="/register" element={<RegisterPage />} />
          <Route path="/forgot" element={<ForgotPasswordPage />} />
          <Route path="/track" element={<TrackingPage />} />
          <Route path="/submit" element={<CustomerOrderPage />} />
          <Route path="/driver" element={<DriverPortalPage />} />
          <Route element={<ProtectedRoute />}>
            <Route element={<AppLayout />}>
              <Route index element={<ControlTowerPage />} />
              <Route path="intake" element={<OrderIntakePage />} />
              <Route path="orders/:id" element={<OrderDetailPage />} />
              <Route path="dispatch-board" element={<DispatchBoardPage />} />
              <Route path="waybills" element={<OrderManagePage />} />
              <Route path="waybills/:no" element={<WaybillDetailPage />} />
              <Route path="fleet" element={<FleetPage />} />
              <Route path="reconciliation" element={<ReconciliationPage />} />
              <Route path="pricing" element={<PricingPage />} />
              <Route path="admin" element={<AdminHubPage />} />
              <Route path="org" element={<OrgCenterPage />} />
              <Route path="profile" element={<ProfilePage />} />
              <Route path="audit" element={<AuditPage />} />
            </Route>
          </Route>
        </Routes>
      </BrowserRouter>
    </AuthProvider>
  );
}
