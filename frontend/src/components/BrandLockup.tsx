import mark from '@/assets/logo-mark.svg';

/** Full logo: vector mark with the company name set in Vazir, sharp at any size. */
export function BrandLockup({ className = '' }: { className?: string }) {
  return (
    <div className={`brand-lockup ${className}`} aria-label="تفکر پردازان — Tafakor ERP" role="img">
      <img src={mark} alt="" className="brand-lockup-mark" />
      <div className="brand-lockup-name" lang="fa">تفکر پردازان</div>
      <div className="brand-lockup-sub">TAFAKOR ERP</div>
    </div>
  );
}
