import { Suspense, useEffect, useState, type ReactNode } from 'react';
import { NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom';
import { aiPage, startAiTagger } from '@/components/ui/aiTags';
import {
  ArrowLeftRight, BarChart3, BookOpen, Boxes, Building2, CalendarRange, ClipboardList, FileClock, FileMinus, FileText,
  Landmark, LayoutDashboard, ListTree, LogOut, Menu, Moon, Package, Receipt, Settings2, Shield, ShoppingCart, Sun,
  Tags, Undo2, UserCog, Printer, Users, Warehouse as WarehouseIcon, Workflow, ScrollText, KeyRound,
  Banknote, IdCard, Calculator, Coins, PiggyBank, ShieldAlert, PieChart, FileBarChart, NotebookTabs, ChevronDown,
} from 'lucide-react';
import logo from '@/assets/logo-mark.svg';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n, type Lang, type TKey } from '@/i18n/I18nProvider';
import { useTheme } from '@/theme/ThemeProvider';
import { api } from '@/api/client';
import { ActionMenu, LoadingBlock } from '@/components/ui/Misc';
import { ErrorBoundary } from '@/components/ErrorBoundary';

interface NavItem { to: string; label: TKey; icon: ReactNode; perm: string; end?: boolean; children?: NavChild[] }
interface NavChild { to: string; label: TKey; perm: string; end?: boolean }
interface NavGroup { title?: TKey; items: NavItem[] }

const groups: NavGroup[] = [
  { items: [{ to: '/', label: 'nav.dashboard', icon: <LayoutDashboard size={18} />, perm: '', end: true }] },
  {
    title: 'nav.accounting',
    items: [
      { to: '/journal', label: 'nav.documents', icon: <BookOpen size={18} />, perm: 'journal.view' },
      { to: '/reports/ledger', label: 'nav.generalLedger', icon: <NotebookTabs size={18} />, perm: 'reports.view' },
      { to: '/accounts', label: 'nav.accounts', icon: <ListTree size={18} />, perm: 'accounts.view' },
      { to: '/parties', label: 'nav.parties', icon: <Users size={18} />, perm: 'parties.view' },
      { to: '/cost-centers', label: 'nav.costCenters', icon: <Tags size={18} />, perm: 'cost_centers.view' },
    ],
  },
  {
    title: 'nav.sales',
    items: [
      { to: '/sales/invoices', label: 'nav.salesInvoices', icon: <FileText size={18} />, perm: 'sales.view' },
      { to: '/sales/quotations', label: 'nav.quotations', icon: <FileClock size={18} />, perm: 'sales.view' },
      { to: '/sales/returns', label: 'nav.salesReturns', icon: <Undo2 size={18} />, perm: 'sales.view' },
    ],
  },
  {
    title: 'nav.purchases',
    items: [
      { to: '/purchases/invoices', label: 'nav.purchaseInvoices', icon: <ShoppingCart size={18} />, perm: 'purchases.view' },
      { to: '/purchases/returns', label: 'nav.purchaseReturns', icon: <FileMinus size={18} />, perm: 'purchases.view' },
    ],
  },
  {
    title: 'nav.inventory',
    items: [
      { to: '/products', label: 'nav.products', icon: <Package size={18} />, perm: 'products.view' },
      { to: '/inventory/stock', label: 'nav.stock', icon: <Boxes size={18} />, perm: 'inventory.view' },
      { to: '/inventory/documents', label: 'nav.stockDocuments', icon: <ClipboardList size={18} />, perm: 'inventory.view' },
      { to: '/inventory/ledger', label: 'nav.stockLedger', icon: <ScrollText size={18} />, perm: 'inventory.view' },
      { to: '/inventory/warehouses', label: 'nav.warehouses', icon: <WarehouseIcon size={18} />, perm: 'inventory.view' },
    ],
  },
  {
    title: 'nav.treasury',
    items: [
      { to: '/treasury', label: 'nav.treasuryAccounts', icon: <Landmark size={18} />, perm: 'treasury.view', end: true },
      { to: '/treasury/transactions', label: 'nav.transactions', icon: <ArrowLeftRight size={18} />, perm: 'treasury.view' },
      { to: '/expenses', label: 'nav.expenses', icon: <Receipt size={18} />, perm: 'expenses.view' },
      {
        to: '/checks', label: 'nav.checks', icon: <Banknote size={18} />, perm: 'checks.view',
        children: [
          { to: '/checks', label: 'nav.checkRegister', perm: 'checks.view', end: true },
          { to: '/checks/dashboard', label: 'nav.checkDashboard', perm: 'checks.view' },
          { to: '/checks/calendar', label: 'nav.checkCalendar', perm: 'checks.view' },
          { to: '/checks/reports', label: 'nav.checkReports', perm: 'checks.view' },
        ],
      },
    ],
  },
  {
    title: 'nav.hr',
    items: [
      { to: '/payroll/employees', label: 'nav.employees', icon: <IdCard size={18} />, perm: 'payroll.view' },
      {
        to: '/payroll', label: 'nav.payroll', icon: <Coins size={18} />, perm: 'payroll.view',
        children: [
          { to: '/payroll', label: 'nav.payrollOverview', perm: 'payroll.view', end: true },
          { to: '/payroll/periods', label: 'nav.payrollRuns', perm: 'payroll.view' },
          { to: '/payroll/reports', label: 'nav.payrollReports', perm: 'payroll.view' },
          { to: '/payroll/settings', label: 'nav.payrollSettings', perm: 'payroll.view' },
        ],
      },
      { to: '/payroll/components', label: 'nav.salaryComponents', icon: <Calculator size={18} />, perm: 'payroll.view' },
    ],
  },
  {
    title: 'nav.budgeting',
    items: [
      { to: '/budgets', label: 'nav.budgetList', icon: <PiggyBank size={18} />, perm: 'budgets.view', end: true },
      { to: '/budgets/dashboard', label: 'nav.budgetDashboard', icon: <PieChart size={18} />, perm: 'budgets.view' },
      { to: '/budgets/control', label: 'nav.budgetControl', icon: <ShieldAlert size={18} />, perm: 'budgets.view' },
      { to: '/budgets/reports', label: 'nav.budgetReports', icon: <FileBarChart size={18} />, perm: 'budgets.view' },
    ],
  },
  {
    title: 'nav.reports',
    items: [{ to: '/reports', label: 'nav.reports', icon: <BarChart3 size={18} />, perm: 'reports.view', end: true }],
  },
  {
    title: 'nav.settings',
    items: [
      { to: '/settings/company', label: 'nav.company', icon: <Building2 size={18} />, perm: 'company.view' },
      { to: '/settings/fiscal-years', label: 'nav.fiscalYears', icon: <CalendarRange size={18} />, perm: 'company.view' },
      { to: '/settings/posting-rules', label: 'nav.mappings', icon: <Workflow size={18} />, perm: 'settings.view' },
      { to: '/settings/letterhead', label: 'nav.letterhead', icon: <Printer size={18} />, perm: 'settings.view' },
      { to: '/settings/users', label: 'nav.users', icon: <UserCog size={18} />, perm: 'users.view' },
      { to: '/settings/roles', label: 'nav.roles', icon: <Shield size={18} />, perm: 'roles.view' },
      { to: '/settings/audit', label: 'nav.audit', icon: <Settings2 size={18} />, perm: 'audit.view' },
    ],
  },
];

/** Whether pathname is inside an item (its own route or any child route). */
function within(pathname: string, item: NavItem) {
  if (item.end) return pathname === item.to;
  return pathname === item.to || pathname.startsWith(`${item.to}/`);
}

/** A section with sub-pages (checks, payroll): expands while one of its pages is open. */
function NavSection({ item, can }: { item: NavItem; can: (p: string) => boolean }) {
  const { t } = useI18n();
  const location = useLocation();
  const children = item.children!.filter((c) => can(c.perm));
  // Sub-routes owned by other sidebar items (e.g. /payroll/employees) do not open the section.
  const owned = within(location.pathname, item) && !['/payroll/employees', '/payroll/components'].some((p) => location.pathname.startsWith(p));
  const [open, setOpen] = useState(owned);
  useEffect(() => { if (owned) setOpen(true); }, [owned]);
  const id = `nav-${item.to.replace(/\W/g, '')}`;
  return (
    <div className={`nav-section ${owned ? 'has-active' : ''}`}>
      <button type="button" className={`nav-link nav-parent ${owned ? 'active-parent' : ''}`} aria-expanded={open} aria-controls={id} onClick={() => setOpen((o) => !o)}>
        {item.icon}
        <span className="grow">{t(item.label)}</span>
        <ChevronDown size={15} className={`nav-chev ${open ? 'open' : ''}`} aria-hidden />
      </button>
      {open && (
        <div id={id} className="nav-children">
          {children.map((c) => (
            <NavLink key={c.to} to={c.to} end={c.end} data-ai-nav={c.to} className={({ isActive }) => `nav-link nav-sub ${isActive ? 'active' : ''}`}>
              <span>{t(c.label)}</span>
            </NavLink>
          ))}
        </div>
      )}
    </div>
  );
}

export function LanguageSwitch() {
  const { lang, setLang } = useI18n();
  const { me } = useAuth();
  const change = (l: Lang) => {
    setLang(l);
    if (me) void api.put('/auth/preferences', { language: l }).catch(() => undefined);
  };
  return (
    <div className="lang-switch" role="group" aria-label="Language">
      <button type="button" lang="fa" aria-pressed={lang === 'fa'} data-ai-action="shell.lang.fa" onClick={() => change('fa')}>فارسی</button>
      <button type="button" lang="en" aria-pressed={lang === 'en'} data-ai-action="shell.lang.en" onClick={() => change('en')}>English</button>
    </div>
  );
}

export function ThemeToggle() {
  const { theme, toggle } = useTheme();
  const { t } = useI18n();
  const { me } = useAuth();
  return (
    <button
      type="button"
      className="btn btn-quiet btn-icon"
      aria-label={t('common.toggleTheme')}
      title={theme === 'dark' ? t('common.light') : t('common.dark')}
      onClick={() => {
        toggle();
        if (me) void api.put('/auth/preferences', { theme: theme === 'dark' ? 'light' : 'dark' }).catch(() => undefined);
      }}
    >
      {theme === 'dark' ? <Sun size={18} /> : <Moon size={18} />}
    </button>
  );
}

export function AppShell() {
  const { me, can, logout } = useAuth();
  const { t, lang } = useI18n();
  const f = { initials: (me?.full_name ?? '?').trim().slice(0, 1) };
  const [navOpen, setNavOpen] = useState(false);
  const location = useLocation();
  const navigate = useNavigate();
  useEffect(() => startAiTagger(), []); // Tafakor AI tags for plain elements (harmless data attributes)
  useEffect(() => setNavOpen(false), [location.pathname]);

  const companyName = lang === 'en' && me?.company.name_en ? me.company.name_en : me?.company.name;

  return (
    <div className={`shell ${navOpen ? 'nav-open' : ''}`}>
      <a href="#main" className="visually-hidden">{t('common.skipToContent')}</a>
      <aside className="sidebar" aria-label={t('common.menu')}>
        <NavLink to="/" className="brand" data-ai-nav="/">
          <img src={logo} alt="" />
          <span>
            <span className="brand-name">{t('app.name')}</span>
            <br />
            <span className="brand-company">{companyName}</span>
          </span>
        </NavLink>
        <nav className="nav" aria-label={t('common.menu')}>
          {groups.map((g, gi) => {
            const items = g.items.filter((i) => !i.perm || can(i.perm));
            if (!items.length) return null;
            return (
              <div key={gi}>
                {g.title && <div className="nav-group-title">{t(g.title)}</div>}
                {items.map((i) => (i.children ? (
                  <NavSection key={i.to} item={i} can={can} />
                ) : (
                  <NavLink key={i.to} to={i.to} end={i.end} data-ai-nav={i.to} className={({ isActive }) => `nav-link ${isActive ? 'active' : ''}`}>
                    {i.icon}
                    <span>{t(i.label)}</span>
                  </NavLink>
                )))}
              </div>
            );
          })}
        </nav>
      </aside>
      <div className="scrim" onClick={() => setNavOpen(false)} />
      <div className="main">
        <header className="header">
          <button type="button" className="btn btn-quiet btn-icon menu-toggle" aria-label={t('common.menu')} onClick={() => setNavOpen(true)}>
            <Menu size={20} />
          </button>
          {me?.fiscal_year && (
            <span className="fy-chip">
              <CalendarRange size={16} />
              {t('common.fiscalYear')} {me.fiscal_year.name}
            </span>
          )}
          <span className="header-spacer" />
          <LanguageSwitch />
          <ThemeToggle />
          <span className="user-chip">
            <span className="avatar" aria-hidden>{f.initials}</span>
            <ActionMenu
              aiKey="shell.userMenu"
              label={me?.full_name}
              items={[
                { label: me?.full_name ?? '', icon: <UserCog size={16} />, to: '/profile' },
                { label: t('auth.changePassword'), icon: <KeyRound size={16} />, to: '/profile' },
                { separator: true, label: '' },
                { label: t('common.logout'), icon: <LogOut size={16} className="icon-directional" />, onClick: async () => { await logout(); navigate('/login'); } },
              ]}
            />
          </span>
        </header>
        <main id="main" className="grow" data-ai-page={aiPage(location.pathname)}>
          <ErrorBoundary resetKey={location.pathname}>
            <Suspense fallback={<LoadingBlock />}>
              <Outlet />
            </Suspense>
          </ErrorBoundary>
        </main>
      </div>
    </div>
  );
}
