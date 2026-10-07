import { useEffect, useId, useRef, useState, type KeyboardEvent, type ReactNode } from 'react';
import { ChevronDown, X } from 'lucide-react';
import { useI18n } from '@/i18n/I18nProvider';
import { useClickOutside, useDebounced } from '@/lib/hooks';
import { Popover } from './Popover';

export interface ComboOption {
  id: number;
  label: string;
  code?: string;
  meta?: ReactNode;
  disabled?: boolean;
}

interface Props {
  value: number | null;
  /** text shown for the current value */
  valueLabel?: string;
  onChange: (id: number | null, option?: ComboOption) => void;
  /** returns options for a query (may be async) */
  search: (q: string) => Promise<ComboOption[]> | ComboOption[];
  /** options shown first when the query is empty */
  recent?: ComboOption[];
  placeholder?: string;
  disabled?: boolean;
  clearable?: boolean;
  autoFocus?: boolean;
  id?: string;
  'aria-invalid'?: boolean;
  'aria-describedby'?: string;
  onKeyDown?: (e: KeyboardEvent<HTMLInputElement>) => void;
  className?: string;
}

/**
 * Searchable, keyboard-first picker: type to filter, ↑/↓ to move, Enter or Tab
 * to choose, Esc to close. Shows recently used items when the query is empty.
 */
export function ComboBox({ value, valueLabel, onChange, search, recent, placeholder, disabled, clearable = true, autoFocus, id, onKeyDown, className = '', ...aria }: Props) {
  const { t } = useI18n();
  const listId = useId();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const [editing, setEditing] = useState(false);
  const [options, setOptions] = useState<ComboOption[]>([]);
  const [active, setActive] = useState(0);
  const [loading, setLoading] = useState(false);
  const debounced = useDebounced(query, 150);
  const inputRef = useRef<HTMLInputElement>(null);
  const pop = useRef<HTMLDivElement>(null);
  const wrap = useClickOutside<HTMLDivElement>(() => close(), open, pop);
  const searchRef = useRef(search);
  searchRef.current = search;

  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    setLoading(true);
    Promise.resolve(searchRef.current(debounced.trim()))
      .then((opts) => {
        if (cancelled) return;
        setOptions(opts);
        setActive(0);
      })
      .finally(() => !cancelled && setLoading(false));
    return () => {
      cancelled = true;
    };
  }, [debounced, open]);

  const showRecent = !debounced.trim() && recent && recent.length > 0;
  const list = showRecent ? [...recent!, ...options.filter((o) => !recent!.some((r) => r.id === o.id))] : options;

  function close() {
    setOpen(false);
    setEditing(false);
    setQuery('');
  }

  function choose(o: ComboOption | undefined) {
    if (!o || o.disabled) return;
    onChange(o.id, o);
    close();
  }

  const handleKey = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      if (!open) setOpen(true);
      setActive((a) => Math.min(a + 1, list.length - 1));
      return;
    }
    if (e.key === 'ArrowUp') {
      e.preventDefault();
      setActive((a) => Math.max(a - 1, 0));
      return;
    }
    if (e.key === 'Enter' && open) {
      e.preventDefault();
      choose(list[active]);
      onKeyDown?.(e);
      return;
    }
    if (e.key === 'Tab' && open && editing && query.trim()) {
      choose(list[active]);
    }
    if (e.key === 'Escape' && open) {
      e.preventDefault();
      e.stopPropagation();
      close();
      return;
    }
    onKeyDown?.(e);
  };

  return (
    <div className={`combo ${className}`} ref={wrap}>
      <div className="input-group">
        <input
          ref={inputRef}
          id={id}
          className="input"
          role="combobox"
          aria-expanded={open}
          aria-controls={listId}
          aria-autocomplete="list"
          autoComplete="off"
          autoFocus={autoFocus}
          disabled={disabled}
          placeholder={placeholder ?? t('common.select')}
          value={editing ? query : valueLabel ?? ''}
          onFocus={() => {
            setEditing(true);
            setQuery('');
            setOpen(true);
          }}
          onBlur={() => {
            // leave time for option clicks
            setTimeout(() => {
              if (!wrap.current?.contains(document.activeElement) && !pop.current?.contains(document.activeElement)) close();
            }, 120);
          }}
          onChange={(e) => {
            setQuery(e.target.value);
            setOpen(true);
          }}
          onKeyDown={handleKey}
          {...aria}
        />
        <span className="input-addon">
          {clearable && value && !disabled ? (
            <button type="button" tabIndex={-1} className="btn btn-quiet btn-icon btn-sm" aria-label={t('common.clear')} onMouseDown={(e) => e.preventDefault()} onClick={() => onChange(null)}>
              <X size={14} />
            </button>
          ) : (
            <ChevronDown size={16} className="muted" style={{ marginInline: 6 }} />
          )}
        </span>
      </div>
      {open && (
        <Popover anchor={wrap} open popoverRef={pop} matchWidth id={listId} role="listbox">
          {showRecent && <div className="option-group">{t('common.recent')}</div>}
          {list.map((o, i) => (
            <div
              key={o.id}
              role="option"
              aria-selected={i === active}
              aria-disabled={o.disabled}
              className="option"
              style={o.disabled ? { opacity: 0.5 } : undefined}
              onMouseDown={(e) => e.preventDefault()}
              onMouseEnter={() => setActive(i)}
              onClick={() => choose(o)}
            >
              {o.code && <span className="option-code num">{o.code}</span>}
              <span>{o.label}</span>
              {o.meta && <span className="option-meta">{o.meta}</span>}
            </div>
          ))}
          {!loading && list.length === 0 && <div className="option-empty">{t('common.noResults')}</div>}
          {loading && list.length === 0 && <div className="option-empty">{t('common.loading')}</div>}
        </Popover>
      )}
    </div>
  );
}
