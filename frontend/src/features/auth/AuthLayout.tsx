import type { ReactNode } from 'react';
import { useI18n } from '@/i18n/I18nProvider';
import { LanguageSwitch, ThemeToggle } from '@/layout/AppShell';
import { BrandLockup } from '@/components/BrandLockup';

export function AuthLayout({ children, wide }: { children: ReactNode; wide?: boolean }) {
  const { t } = useI18n();
  return (
    <div className="auth-page">
      <div className="auth-brand">
        <BrandLockup />
        <p className="tagline">{t('app.tagline')}</p>
        <p className="foot">{t('app.name')}</p>
      </div>
      <div className="auth-form-wrap">
        <div className="top">
          <LanguageSwitch />
          <ThemeToggle />
        </div>
        <div className={`auth-form ${wide ? 'wide' : ''}`}>{children}</div>
      </div>
    </div>
  );
}
