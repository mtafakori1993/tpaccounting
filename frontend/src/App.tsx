import { lazy, Suspense, useEffect, useRef, type ReactNode } from 'react';
import { BrowserRouter, Navigate, Route, Routes, useLocation } from 'react-router-dom';
import { QueryClient, QueryClientProvider, useQuery } from '@tanstack/react-query';
import { ApiError, api } from '@/api/client';
import { AuthProvider, useAuth } from '@/auth/AuthProvider';
import { I18nProvider, useI18n } from '@/i18n/I18nProvider';
import { ThemeProvider, useTheme } from '@/theme/ThemeProvider';
import { ToastProvider } from '@/components/ui/Toast';
import { LoadingBlock } from '@/components/ui/Misc';
import { AppShell } from '@/layout/AppShell';
import { LoginPage } from '@/features/auth/LoginPage';
import { SetupPage } from '@/features/auth/SetupPage';
import { BudgetPrompt } from '@/features/budgets/BudgetPrompt';

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: (count, err) => !(err instanceof ApiError && err.status >= 400 && err.status < 500) && count < 2,
      refetchOnWindowFocus: false,
    },
  },
});

const page = <T extends Record<string, unknown>>(loader: () => Promise<T>, name: keyof T) =>
  lazy(() => loader().then((m) => ({ default: m[name] as React.ComponentType })));

const Dashboard = page(() => import('@/features/dashboard/DashboardPage'), 'DashboardPage');
const ChartPage = page(() => import('@/features/accounts/ChartPage'), 'ChartPage');
const PartiesPage = page(() => import('@/features/parties/PartiesPage'), 'PartiesPage');
const PartyPage = page(() => import('@/features/parties/PartyPage'), 'PartyPage');
const JournalListPage = page(() => import('@/features/journal/JournalListPage'), 'JournalListPage');
const JournalFormPage = page(() => import('@/features/journal/JournalFormPage'), 'JournalFormPage');
const JournalViewPage = page(() => import('@/features/journal/JournalViewPage'), 'JournalViewPage');
const InvoiceListPage = page(() => import('@/features/invoices/InvoiceListPage'), 'InvoiceListPage');
const InvoiceFormPage = page(() => import('@/features/invoices/InvoiceFormPage'), 'InvoiceFormPage');
const InvoiceViewPage = page(() => import('@/features/invoices/InvoiceViewPage'), 'InvoiceViewPage');
const ProductsPage = page(() => import('@/features/products/ProductsPage'), 'ProductsPage');
const StockPage = page(() => import('@/features/inventory/StockPage'), 'StockPage');
const StockDocsPage = page(() => import('@/features/inventory/StockDocsPage'), 'StockDocsPage');
const StockDocFormPage = page(() => import('@/features/inventory/StockDocFormPage'), 'StockDocFormPage');
const StockDocViewPage = page(() => import('@/features/inventory/StockDocViewPage'), 'StockDocViewPage');
const StockLedgerPage = page(() => import('@/features/inventory/StockLedgerPage'), 'StockLedgerPage');
const WarehousesPage = page(() => import('@/features/inventory/WarehousesPage'), 'WarehousesPage');
const TreasuryAccountsPage = page(() => import('@/features/treasury/TreasuryAccountsPage'), 'TreasuryAccountsPage');
const TransactionsPage = page(() => import('@/features/treasury/TransactionsPage'), 'TransactionsPage');
const ExpensesPage = page(() => import('@/features/expenses/ExpensesPage'), 'ExpensesPage');
const CostCentersPage = page(() => import('@/features/costcenters/CostCentersPage'), 'CostCentersPage');
const ReportsHome = page(() => import('@/features/reports/ReportsHome'), 'ReportsHome');
const ReportPage = page(() => import('@/features/reports/ReportPage'), 'ReportPage');
const CompanyPage = page(() => import('@/features/settings/CompanyPage'), 'CompanyPage');
const FiscalYearsPage = page(() => import('@/features/settings/FiscalYearsPage'), 'FiscalYearsPage');
const MappingsPage = page(() => import('@/features/settings/MappingsPage'), 'MappingsPage');
const LetterheadPage = page(() => import('@/features/settings/LetterheadPage'), 'LetterheadPage');
const UsersPage = page(() => import('@/features/settings/UsersPage'), 'UsersPage');
const RolesPage = page(() => import('@/features/settings/RolesPage'), 'RolesPage');
const AuditPage = page(() => import('@/features/settings/AuditPage'), 'AuditPage');
const ProfilePage = page(() => import('@/features/settings/ProfilePage'), 'ProfilePage');
const ChecksDashboardPage = page(() => import('@/features/checks/ChecksDashboardPage'), 'ChecksDashboardPage');
const ChecksListPage = page(() => import('@/features/checks/ChecksListPage'), 'ChecksListPage');
const ChecksCalendarPage = page(() => import('@/features/checks/ChecksCalendarPage'), 'ChecksCalendarPage');
const CheckReportPage = page(() => import('@/features/checks/CheckReportPage'), 'CheckReportPage');
const CheckDetailPage = page(() => import('@/features/checks/CheckDetailPage'), 'CheckDetailPage');
const CheckFormPage = page(() => import('@/features/checks/CheckFormPage'), 'CheckFormPage');
const EmployeesPage = page(() => import('@/features/payroll/EmployeesPage'), 'EmployeesPage');
const ComponentsPage = page(() => import('@/features/payroll/ComponentsPage'), 'ComponentsPage');
const PayrollPeriodsPage = page(() => import('@/features/payroll/PayrollPeriodsPage'), 'PayrollPeriodsPage');
const PayrollPeriodPage = page(() => import('@/features/payroll/PayrollPeriodPage'), 'PayrollPeriodPage');
const PayrollReportPage = page(() => import('@/features/payroll/PayrollReportPage'), 'PayrollReportPage');
const PayrollOverviewPage = page(() => import('@/features/payroll/PayrollOverviewPage'), 'PayrollOverviewPage');
const PayrollSettingsPage = page(() => import('@/features/payroll/PayrollSettingsPage'), 'PayrollSettingsPage');
const BudgetsPage = page(() => import('@/features/budgets/BudgetsPage'), 'BudgetsPage');
const BudgetFormPage = page(() => import('@/features/budgets/BudgetFormPage'), 'BudgetFormPage');
const BudgetViewPage = page(() => import('@/features/budgets/BudgetViewPage'), 'BudgetViewPage');
const BudgetDashboardPage = page(() => import('@/features/budgets/BudgetDashboardPage'), 'BudgetDashboardPage');
const BudgetControlPage = page(() => import('@/features/budgets/BudgetControlPage'), 'BudgetControlPage');
const BudgetReportPage = page(() => import('@/features/budgets/BudgetReportPage'), 'BudgetReportPage');

function useSetupStatus() {
  return useQuery({ queryKey: ['setup-status'], queryFn: () => api.get<{ needs_setup: boolean }>('/setup/status'), staleTime: Infinity });
}

/** Applies the signed-in user's saved language and theme once per session. */
function PreferenceSync() {
  const { me } = useAuth();
  const { setLang } = useI18n();
  const { setTheme } = useTheme();
  const applied = useRef<number | null>(null);
  useEffect(() => {
    if (me && applied.current !== me.id) {
      applied.current = me.id;
      setLang(me.language);
      setTheme(me.theme);
    }
  }, [me, setLang, setTheme]);
  return null;
}

function Guard({ children }: { children: ReactNode }) {
  const { me, loading } = useAuth();
  const setup = useSetupStatus();
  const location = useLocation();
  if (loading || setup.isLoading) return <LoadingBlock />;
  if (setup.data?.needs_setup) return <Navigate to="/setup" replace />;
  if (!me) return <Navigate to="/login" replace state={{ from: location.pathname }} />;
  return <>{children}</>;
}

function PublicOnly({ children, setupPage }: { children: ReactNode; setupPage?: boolean }) {
  const { me, loading } = useAuth();
  const setup = useSetupStatus();
  if (loading || setup.isLoading) return <LoadingBlock />;
  if (setup.data?.needs_setup && !setupPage) return <Navigate to="/setup" replace />;
  if (!setup.data?.needs_setup && setupPage) return <Navigate to="/login" replace />;
  if (me) return <Navigate to="/" replace />;
  return <>{children}</>;
}

function AppRoutes() {
  return (
    <Suspense fallback={<LoadingBlock />}>
      <Routes>
        <Route path="/login" element={<PublicOnly><LoginPage /></PublicOnly>} />
        <Route path="/setup" element={<PublicOnly setupPage><SetupPage /></PublicOnly>} />
        <Route element={<Guard><AppShell /></Guard>}>
          <Route index element={<Dashboard />} />
          <Route path="accounts" element={<ChartPage />} />
          <Route path="parties" element={<PartiesPage />} />
          <Route path="parties/:id" element={<PartyPage />} />
          <Route path="journal" element={<JournalListPage />} />
          <Route path="journal/new" element={<JournalFormPage />} />
          <Route path="journal/:id" element={<JournalViewPage />} />
          <Route path="journal/:id/edit" element={<JournalFormPage />} />
          <Route path="checks" element={<ChecksListPage />} />
          <Route path="checks/list" element={<Navigate to="/checks" replace />} />
          <Route path="checks/new" element={<CheckFormPage />} />
          <Route path="checks/dashboard" element={<ChecksDashboardPage />} />
          <Route path="checks/calendar" element={<ChecksCalendarPage />} />
          <Route path="checks/reports" element={<CheckReportPage />} />
          <Route path="checks/reports/:report" element={<CheckReportPage />} />
          <Route path="checks/:id" element={<CheckDetailPage />} />
          <Route path="payroll" element={<PayrollOverviewPage />} />
          <Route path="payroll/periods" element={<PayrollPeriodsPage />} />
          <Route path="payroll/settings" element={<PayrollSettingsPage />} />
          <Route path="payroll/employees" element={<EmployeesPage />} />
          <Route path="payroll/components" element={<ComponentsPage />} />
          <Route path="payroll/periods/:id" element={<PayrollPeriodPage />} />
          <Route path="payroll/reports" element={<PayrollReportPage />} />
          <Route path="payroll/reports/:report" element={<PayrollReportPage />} />
          <Route path="budgets" element={<BudgetsPage />} />
          <Route path="budgets/new" element={<BudgetFormPage />} />
          <Route path="budgets/dashboard" element={<BudgetDashboardPage />} />
          <Route path="budgets/control" element={<BudgetControlPage />} />
          <Route path="budgets/reports" element={<BudgetReportPage />} />
          <Route path="budgets/reports/:report" element={<BudgetReportPage />} />
          <Route path="budgets/:id" element={<BudgetViewPage />} />
          <Route path="budgets/:id/edit" element={<BudgetFormPage />} />
          <Route path=":side/:kind" element={<InvoiceListPage />} />
          <Route path=":side/:kind/new" element={<InvoiceFormPage />} />
          <Route path=":side/:kind/:id" element={<InvoiceViewPage />} />
          <Route path=":side/:kind/:id/edit" element={<InvoiceFormPage />} />
          <Route path="products" element={<ProductsPage />} />
          <Route path="inventory/stock" element={<StockPage />} />
          <Route path="inventory/documents" element={<StockDocsPage />} />
          <Route path="inventory/documents/new" element={<StockDocFormPage />} />
          <Route path="inventory/documents/:id" element={<StockDocViewPage />} />
          <Route path="inventory/documents/:id/edit" element={<StockDocFormPage />} />
          <Route path="inventory/ledger" element={<StockLedgerPage />} />
          <Route path="inventory/warehouses" element={<WarehousesPage />} />
          <Route path="treasury" element={<TreasuryAccountsPage />} />
          <Route path="treasury/transactions" element={<TransactionsPage />} />
          <Route path="expenses" element={<ExpensesPage />} />
          <Route path="cost-centers" element={<CostCentersPage />} />
          <Route path="reports" element={<ReportsHome />} />
          <Route path="reports/:report" element={<ReportPage />} />
          <Route path="settings/company" element={<CompanyPage />} />
          <Route path="settings/fiscal-years" element={<FiscalYearsPage />} />
          <Route path="settings/posting-rules" element={<MappingsPage />} />
          <Route path="settings/letterhead" element={<LetterheadPage />} />
          <Route path="settings/users" element={<UsersPage />} />
          <Route path="settings/roles" element={<RolesPage />} />
          <Route path="settings/audit" element={<AuditPage />} />
          <Route path="profile" element={<ProfilePage />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Route>
      </Routes>
    </Suspense>
  );
}

export function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <ThemeProvider>
        <I18nProvider>
          <ToastProvider>
            <AuthProvider>
              <PreferenceSync />
              <BudgetPrompt />
              <BrowserRouter>
                <AppRoutes />
              </BrowserRouter>
            </AuthProvider>
          </ToastProvider>
        </I18nProvider>
      </ThemeProvider>
    </QueryClientProvider>
  );
}
