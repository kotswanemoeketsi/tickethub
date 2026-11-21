import { lazy, Suspense } from 'react'
import { Route, Routes } from 'react-router'
import { PublicLayout } from './layouts/PublicLayout'
import { RequireAuth } from './layouts/guards'
import { EmptyState, ButtonLink, Loading } from './components/ui'
import { P } from './lib/permissions'
import { HomePage } from './pages/public/HomePage'
import { EventPage } from './pages/public/EventPage'
import { CheckoutPage } from './pages/public/CheckoutPage'
import { PaymentReturnPage } from './pages/public/PaymentReturnPage'
import { AuthCompletePage, ForgotPasswordPage, LoginPage, RegisterPage, ResetPasswordPage } from './pages/public/AuthPages'
import { BrowsePage } from './pages/public/BrowsePage'
import { HelpPage, SellPage, TermsPage } from './pages/public/InfoPages'
import { BecomeOrganiserPage } from './pages/public/BecomeOrganiserPage'
import { MyTicketsPage } from './pages/account/MyTicketsPage'
import { MyOrdersPage } from './pages/account/MyOrdersPage'
import { OrderPage } from './pages/account/OrderPage'
import { ProfilePage } from './pages/account/ProfilePage'
import { AppStatus } from './components/AppStatus'

// The portal and the scanner are loaded on demand, so buyers download only the marketplace.
const AdminLayout = lazy(() => import('./layouts/AdminLayout').then((m) => ({ default: m.AdminLayout })))
const DashboardPage = lazy(() => import('./pages/admin/DashboardPage'))
const EventsPage = lazy(() => import('./pages/admin/EventsPage'))
const EventFormPage = lazy(() => import('./pages/admin/EventFormPage'))
const EventDetailPage = lazy(() => import('./pages/admin/EventDetailPage'))
const OrdersPage = lazy(() => import('./pages/admin/OrdersPage'))
const AdminOrderPage = lazy(() => import('./pages/admin/AdminOrderPage'))
const PaymentsPage = lazy(() => import('./pages/admin/PaymentsPage'))
const RefundsPage = lazy(() => import('./pages/admin/RefundsPage'))
const ReportsPage = lazy(() => import('./pages/admin/ReportsPage'))
const OrganisersPage = lazy(() => import('./pages/admin/OrganisersPage'))
const OrganiserDetailPage = lazy(() => import('./pages/admin/OrganiserDetailPage'))
const UsersPage = lazy(() => import('./pages/admin/UsersPage'))
const RolesPage = lazy(() => import('./pages/admin/RolesPage'))
const CataloguePage = lazy(() => import('./pages/admin/CataloguePage'))
const AuditPage = lazy(() => import('./pages/admin/AuditPage'))
const ScannerPage = lazy(() => import('./pages/scanner/ScannerPage'))

function NotFound() {
  return <EmptyState title="Page not found" description="The page you are looking for does not exist." action={<ButtonLink to="/">Browse events</ButtonLink>} />
}

export function App() {
  return (
    <Suspense fallback={<Loading />}>
      <AppStatus />
      <Routes>
        <Route element={<PublicLayout />}>
          <Route index element={<HomePage />} />
          <Route path="events" element={<BrowsePage />} />
          <Route path="events/:slug" element={<EventPage />} />
          <Route path="sell" element={<SellPage />} />
          <Route path="help" element={<HelpPage />} />
          <Route path="terms" element={<TermsPage />} />
          <Route path="auth/complete" element={<AuthCompletePage />} />
          <Route path="login" element={<LoginPage />} />
          <Route path="register" element={<RegisterPage />} />
          <Route path="forgot-password" element={<ForgotPasswordPage />} />
          <Route path="reset-password" element={<ResetPasswordPage />} />
          <Route path="checkout/:orderId" element={<RequireAuth><CheckoutPage /></RequireAuth>} />
          <Route path="orders/:orderId/payment-return" element={<RequireAuth><PaymentReturnPage /></RequireAuth>} />
          <Route path="organisers/apply" element={<RequireAuth><BecomeOrganiserPage /></RequireAuth>} />
          <Route path="account/tickets" element={<RequireAuth offlineFallback={<MyTicketsPage offline />}><MyTicketsPage /></RequireAuth>} />
          <Route path="account/orders" element={<RequireAuth><MyOrdersPage /></RequireAuth>} />
          <Route path="account/orders/:orderId" element={<RequireAuth><OrderPage /></RequireAuth>} />
          <Route path="account/profile" element={<RequireAuth><ProfilePage /></RequireAuth>} />
          <Route path="*" element={<NotFound />} />
        </Route>

        <Route path="scan" element={<RequireAuth anyOf={[P.CheckinScan]}><ScannerPage /></RequireAuth>} />

        <Route path="admin" element={<RequireAuth staff><AdminLayout /></RequireAuth>}>
          <Route index element={<RequireAuth anyOf={[P.DashboardView]}><DashboardPage /></RequireAuth>} />
          <Route path="events" element={<RequireAuth anyOf={[P.EventsView]}><EventsPage /></RequireAuth>} />
          <Route path="approvals" element={<RequireAuth anyOf={[P.EventsReview]}><EventsPage reviewQueue /></RequireAuth>} />
          <Route path="events/new" element={<RequireAuth anyOf={[P.EventsCreate]}><EventFormPage /></RequireAuth>} />
          <Route path="events/:eventId" element={<RequireAuth anyOf={[P.EventsView]}><EventDetailPage /></RequireAuth>} />
          <Route path="events/:eventId/edit" element={<RequireAuth anyOf={[P.EventsUpdate]}><EventFormPage /></RequireAuth>} />
          <Route path="orders" element={<RequireAuth anyOf={[P.OrdersView]}><OrdersPage /></RequireAuth>} />
          <Route path="orders/:orderId" element={<RequireAuth anyOf={[P.OrdersView]}><AdminOrderPage /></RequireAuth>} />
          <Route path="payments" element={<RequireAuth anyOf={[P.PaymentsView]}><PaymentsPage /></RequireAuth>} />
          <Route path="refunds" element={<RequireAuth anyOf={[P.RefundsView]}><RefundsPage /></RequireAuth>} />
          <Route path="reports" element={<RequireAuth anyOf={[P.ReportsView, P.ReportsFinancial]}><ReportsPage /></RequireAuth>} />
          <Route path="organisers" element={<RequireAuth anyOf={[P.OrganisersView]}><OrganisersPage /></RequireAuth>} />
          <Route path="organisers/:organiserId" element={<OrganiserDetailPage />} />
          <Route path="users" element={<RequireAuth anyOf={[P.UsersView]}><UsersPage /></RequireAuth>} />
          <Route path="roles" element={<RequireAuth anyOf={[P.RolesManage]}><RolesPage /></RequireAuth>} />
          <Route path="catalogue" element={<RequireAuth anyOf={[P.CategoriesManage, P.VenuesManage]}><CataloguePage /></RequireAuth>} />
          <Route path="audit" element={<RequireAuth anyOf={[P.AuditView]}><AuditPage /></RequireAuth>} />
          <Route path="*" element={<NotFound />} />
        </Route>
      </Routes>
    </Suspense>
  )
}
