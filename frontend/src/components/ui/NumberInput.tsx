import { useLayoutEffect, useRef, useState, type InputHTMLAttributes, type KeyboardEvent } from 'react';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { applyNumericEdit, deleteAcrossSeparator, inputDisplay, parseFormattedNumber } from '@/lib/number';

export interface NumericInputProps extends Omit<InputHTMLAttributes<HTMLInputElement>, 'value' | 'onChange'> {
  /** plain decimal string ("1250000", "-12.5") or '' */
  value: string;
  onChange: (v: string) => void;
  /** max fraction digits (default 0) */
  fraction?: number;
  allowNegative?: boolean;
  /**
   * Lets the field also hold a short expression the caller interprets:
   * "10%" (a percentage, e.g. invoice discounts) or "=" (balance the entry).
   */
  allowExpression?: boolean;
  /** text shown after the number inside the field, e.g. "%" */
  suffix?: string;
}

/**
 * Numeric entry with thousands grouping while typing. The value given to
 * onChange is always a plain decimal string for the API — never the formatted
 * text. The caret stays on the digit being edited; Backspace/Delete next to a
 * separator remove the digit beyond it; pasted "1,250,000", "1.250.000",
 * "۱٬۲۵۰٬۰۰۰" or "1 250 000" become 1250000.
 */
export function NumericInput({ value, onChange, fraction = 0, allowNegative, allowExpression, suffix, className = '', onBlur, onKeyDown, onPaste, ...rest }: NumericInputProps) {
  const { lang } = useI18n();
  const ref = useRef<HTMLInputElement>(null);
  const pendingCaret = useRef<number | null>(null);
  const isExpr = allowExpression && (value.endsWith('%') || value === '=');
  // What the user sees. It is re-derived when the value changes from outside
  // (or the language changes), but not when the change is our own echo, so a
  // partial entry like "1,250." survives the round trip.
  const [text, setText] = useState(() => (isExpr ? value : inputDisplay(value, lang)));
  const [prev, setPrev] = useState({ value, lang });
  const [, rerender] = useState(0);
  if (prev.value !== value || prev.lang !== lang) {
    setPrev({ value, lang });
    const ours = text === value || parseFormattedNumber(text, { maxFraction: fraction, allowNegative: true }) === value;
    if (prev.lang !== lang || !ours) setText(isExpr ? value : inputDisplay(value, lang));
  }
  useLayoutEffect(() => {
    if (pendingCaret.current !== null && ref.current && document.activeElement === ref.current) {
      ref.current.setSelectionRange(pendingCaret.current, pendingCaret.current);
    }
    pendingCaret.current = null;
  });

  const commit = (next: string, caret: number) => {
    if (allowExpression) {
      const plain = next.replace(/\s/g, '');
      if (plain === '=' || /^[\d۰-۹٠-٩.,٫٬]*%$/.test(plain)) {
        const v = plain === '=' ? '=' : `${parseFormattedNumber(plain.slice(0, -1), { maxFraction: 4 }) ?? ''}%`;
        setText(plain);
        onChange(v);
        return;
      }
    }
    const r = applyNumericEdit(next, caret, { lang, maxFraction: fraction, allowNegative });
    if (!r) {
      // Rejected keystroke: keep the text and put the caret back.
      pendingCaret.current = Math.max(0, caret - (next.length - text.length));
      rerender((n) => n + 1);
      return;
    }
    setText(r.display);
    pendingCaret.current = r.caret;
    const v = r.raw.replace(/\.$/, '').replace(/^-$/, '');
    if (v !== value) onChange(v);
  };

  const handleKey = (e: KeyboardEvent<HTMLInputElement>) => {
    onKeyDown?.(e);
    if (e.defaultPrevented) return;
    if (e.key === 'Backspace' || e.key === 'Delete') {
      const el = e.currentTarget;
      const d = deleteAcrossSeparator(el.value, el.selectionStart ?? 0, el.selectionEnd ?? 0, e.key, lang);
      if (d) {
        e.preventDefault();
        commit(d.text, d.caret);
      }
    }
  };

  return (
    <span className={`num-input ${suffix ? 'has-suffix' : ''}`}>
      <input
        ref={ref}
        inputMode={fraction > 0 ? 'decimal' : 'numeric'}
        autoComplete="off"
        className={`input num ${className}`}
        value={text}
        onChange={(e) => commit(e.target.value, e.target.selectionStart ?? e.target.value.length)}
        onKeyDown={handleKey}
        onPaste={(e) => {
          onPaste?.(e);
          if (e.defaultPrevented) return;
          // Pasted text is parsed as a whole (it may use another locale's separators).
          const pasted = e.clipboardData.getData('text');
          const el = e.currentTarget;
          const start = el.selectionStart ?? 0;
          const end = el.selectionEnd ?? 0;
          if (start === 0 && end === el.value.length) {
            const v = parseFormattedNumber(pasted, { maxFraction: fraction, allowNegative });
            if (v !== null) {
              e.preventDefault();
              const display = inputDisplay(v, lang);
              setText(display);
              pendingCaret.current = display.length;
              onChange(v);
            }
          }
        }}
        onBlur={(e) => {
          // A trailing point or lone minus is dropped when leaving the field.
          if (!isExpr) {
            const v = parseFormattedNumber(text, { maxFraction: fraction, allowNegative: true });
            if (v !== null) setText(inputDisplay(v, lang));
          }
          onBlur?.(e);
        }}
        {...rest}
      />
      {suffix && <span className="num-suffix" aria-hidden>{suffix}</span>}
    </span>
  );
}

/** Money entry: the company's amount decimals, no negatives unless asked. */
export function MoneyInput(props: Omit<NumericInputProps, 'fraction'> & { fraction?: number }) {
  const f = useFormat();
  return <NumericInput fraction={props.fraction ?? f.decimals} {...props} />;
}

/** A percentage (e.g. 15 or 12.5) with a % sign; the value excludes the sign. */
export function PercentageInput({ fraction = 2, ...props }: Omit<NumericInputProps, 'suffix'>) {
  const { lang } = useI18n();
  return <NumericInput fraction={fraction} suffix={lang === 'fa' ? '٪' : '%'} {...props} />;
}
