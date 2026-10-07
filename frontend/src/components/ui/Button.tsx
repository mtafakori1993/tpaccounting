import { forwardRef, type ButtonHTMLAttributes, type ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { aiKey } from './aiTags';

type Variant = 'accent' | 'secondary' | 'quiet' | 'negative';

interface Props extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant;
  size?: 'sm' | 'md';
  icon?: ReactNode;
  pending?: boolean;
}

export const Button = forwardRef<HTMLButtonElement, Props>(function Button(
  { variant = 'secondary', size = 'md', icon, pending, children, className = '', type = 'button', disabled, ...rest },
  ref,
) {
  return (
    <button
      ref={ref}
      type={type}
      className={`btn btn-${variant} ${size === 'sm' ? 'btn-sm' : ''} ${className}`}
      disabled={disabled || pending}
      aria-busy={pending || undefined}
      data-ai-action={aiKey(children)}
      {...rest}
    >
      {pending ? <span className="spinner" style={{ width: 14, height: 14 }} /> : icon}
      {children}
    </button>
  );
});

export function IconButton({ label, icon, size = 'md', className = '', ...rest }: Omit<Props, 'children'> & { label: string; icon: ReactNode }) {
  return (
    <button type="button" className={`btn btn-quiet btn-icon ${size === 'sm' ? 'btn-sm' : ''} ${className}`} aria-label={label} title={label} data-ai-action={aiKey(label)} {...rest}>
      {icon}
    </button>
  );
}

export function LinkButton({ to, variant = 'secondary', icon, children, size = 'md' }: { to: string; variant?: Variant; icon?: ReactNode; children: ReactNode; size?: 'sm' | 'md' }) {
  return (
    <Link to={to} className={`btn btn-${variant} ${size === 'sm' ? 'btn-sm' : ''}`} data-ai-action={aiKey(children)} data-ai-nav={to}>
      {icon}
      {children}
    </Link>
  );
}
