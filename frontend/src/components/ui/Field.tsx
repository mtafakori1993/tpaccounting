import { useId, type InputHTMLAttributes, type ReactNode, type SelectHTMLAttributes, type TextareaHTMLAttributes, cloneElement, isValidElement, type ReactElement } from 'react';
import { useFieldError } from '@/i18n/errors';
import { normalizeDigits } from '@/lib/dates';
import { useFormat } from '@/i18n/format';
import { NumericInput, type NumericInputProps } from './NumberInput';
import { aiKey } from './aiTags';

interface FieldProps {
  label?: ReactNode;
  required?: boolean;
  help?: ReactNode;
  /** validation code from the API, translated automatically */
  error?: string;
  className?: string;
  /** explicit data-ai-field tag (defaults to the label's i18n key) */
  aiKey?: string;
  children: ReactElement<{ id?: string; 'aria-invalid'?: boolean; 'aria-describedby'?: string }>;
}

/** Label + control + help/error, wired for accessibility. */
export function Field({ label, required, help, error, className = '', aiKey: tag, children }: FieldProps) {
  const id = useId();
  const fieldError = useFieldError();
  const msg = fieldError(error);
  const describedBy = msg || help ? `${id}-desc` : undefined;
  const control = isValidElement(children)
    ? cloneElement(children, { id: children.props.id ?? id, 'aria-invalid': msg ? true : undefined, 'aria-describedby': describedBy })
    : children;
  return (
    <div className={`field ${className}`} data-ai-field={tag ?? aiKey(label)}>
      {label && (
        <label className="field-label" htmlFor={children.props.id ?? id}>
          {label}
          {required && <span className="req" aria-hidden>*</span>}
        </label>
      )}
      {control}
      {msg ? (
        <span id={describedBy} className="field-error">{msg}</span>
      ) : help ? (
        <span id={describedBy} className="field-help">{help}</span>
      ) : null}
    </div>
  );
}

export function Input({ className = '', ...rest }: InputHTMLAttributes<HTMLInputElement>) {
  return <input className={`input ${className}`} {...rest} />;
}

export function TextArea({ className = '', ...rest }: TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return <textarea className={`textarea ${className}`} {...rest} />;
}

export function Select({ className = '', children, ...rest }: SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select className={`select ${className}`} {...rest}>
      {children}
    </select>
  );
}

export function Checkbox({ label, checked, onChange, disabled, aiKey: tag }: { label: ReactNode; checked: boolean; onChange: (v: boolean) => void; disabled?: boolean; aiKey?: string }) {
  return (
    <label className="checkbox" data-ai-field={tag ?? aiKey(label)}>
      <input type="checkbox" checked={checked} disabled={disabled} onChange={(e) => onChange(e.target.checked)} />
      <span>{label}</span>
    </label>
  );
}

export function Switch({ label, checked, onChange, disabled }: { label: ReactNode; checked: boolean; onChange: (v: boolean) => void; disabled?: boolean }) {
  return (
    <label className="switch" data-ai-field={aiKey(label)}>
      <input type="checkbox" role="switch" checked={checked} disabled={disabled} onChange={(e) => onChange(e.target.checked)} />
      <span>{label}</span>
    </label>
  );
}

/** Cleans typed numeric text: Persian digits → ASCII, strip separators. */
export function cleanNumber(s: string) {
  return normalizeDigits(s).replace(/[,\s]/g, '');
}

interface AmountProps extends Omit<NumericInputProps, 'fraction' | 'allowExpression'> {
  /** max fraction digits; defaults to company amount decimals */
  fraction?: number;
}

/**
 * Amount entry that also accepts "10%" (a percentage the caller converts,
 * e.g. invoice discounts) and "=" (balance a journal line). For plain amounts
 * use MoneyInput; for quantities NumericInput; for rates PercentageInput.
 */
export function AmountInput({ fraction, ...rest }: AmountProps) {
  const f = useFormat();
  return <NumericInput fraction={fraction ?? f.decimals} allowExpression {...rest} />;
}

export { MoneyInput, NumericInput, PercentageInput } from './NumberInput';
