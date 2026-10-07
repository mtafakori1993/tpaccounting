import { useEffect, useMemo, useRef, useState, type InputHTMLAttributes } from 'react';
import { CalendarDays, ChevronLeft, ChevronRight } from 'lucide-react';
import { useFormat } from '@/i18n/format';
import { useI18n } from '@/i18n/I18nProvider';
import { calendarParts, formatCalendarDate, isoFromCalendar, monthLength, parseCalendarDate, todayIso } from '@/lib/dates';
import { useClickOutside } from '@/lib/hooks';
import { Popover } from './Popover';

const jalaliMonths = ['فروردین', 'اردیبهشت', 'خرداد', 'تیر', 'مرداد', 'شهریور', 'مهر', 'آبان', 'آذر', 'دی', 'بهمن', 'اسفند'];
const jalaliMonthsEn = ['Farvardin', 'Ordibehesht', 'Khordad', 'Tir', 'Mordad', 'Shahrivar', 'Mehr', 'Aban', 'Azar', 'Dey', 'Bahman', 'Esfand'];

interface Props extends Omit<InputHTMLAttributes<HTMLInputElement>, 'value' | 'onChange'> {
  value: string;
  onChange: (iso: string) => void;
}

/** Date input in the company calendar. Accepts typing (e.g. 1405/7/14) or picking. */
export function DateField({ value, onChange, className = '', ...rest }: Props) {
  const f = useFormat();
  const { lang, t } = useI18n();
  const cal = f.calendar;
  const [text, setText] = useState(value ? f.digits(formatCalendarDate(value, cal)) : '');
  const [invalid, setInvalid] = useState(false);
  const [open, setOpen] = useState(false);
  const pop = useRef<HTMLDivElement>(null);
  const ref = useClickOutside<HTMLDivElement>(() => setOpen(false), open, pop);

  useEffect(() => {
    setText(value ? f.digits(formatCalendarDate(value, cal)) : '');
    setInvalid(false);
  }, [value, cal, f]);

  const commit = () => {
    if (!text.trim()) {
      setInvalid(false);
      if (value) onChange('');
      return;
    }
    const iso = parseCalendarDate(text, cal);
    if (iso) {
      setInvalid(false);
      if (iso !== value) onChange(iso);
      setText(f.digits(formatCalendarDate(iso, cal)));
    } else setInvalid(true);
  };

  return (
    <div className="combo" ref={ref}>
      <div className="input-group">
        <input
          className={`input num ${className}`}
          value={text}
          placeholder={cal === 'jalali' ? f.digits('1405/01/01') : '2026-01-01'}
          onChange={(e) => setText(e.target.value)}
          onBlur={commit}
          onKeyDown={(e) => {
            if (e.key === 'Enter') commit();
            if (e.key === 'ArrowDown' && e.altKey) setOpen(true);
          }}
          aria-invalid={invalid || rest['aria-invalid'] ? true : undefined}
          {...rest}
        />
        <span className="input-addon">
          <button type="button" className="btn btn-quiet btn-icon btn-sm" aria-label={t('common.select')} onClick={() => setOpen((o) => !o)} tabIndex={-1}>
            <CalendarDays size={16} />
          </button>
        </span>
      </div>
      {open && (
        <Popover anchor={ref} open popoverRef={pop} className="calendar" role="dialog">
          <CalendarGrid
            value={value || todayIso()}
            onPick={(iso) => {
              onChange(iso);
              setOpen(false);
            }}
            monthNames={cal === 'jalali' ? (lang === 'fa' ? jalaliMonths : jalaliMonthsEn) : undefined}
          />
          <div className="row" style={{ justifyContent: 'center', marginTop: 6 }}>
            <button type="button" className="btn btn-quiet btn-sm" onClick={() => { onChange(todayIso()); setOpen(false); }}>{t('common.today')}</button>
          </div>
        </Popover>
      )}
    </div>
  );
}

function CalendarGrid({ value, onPick, monthNames }: { value: string; onPick: (iso: string) => void; monthNames?: string[] }) {
  const f = useFormat();
  const { lang } = useI18n();
  const cal = f.calendar;
  const start = calendarParts(value, cal);
  const [ym, setYm] = useState({ y: start.y, m: start.m });
  const today = todayIso();

  const days = useMemo(() => {
    const first = isoFromCalendar(ym.y, ym.m, 1, cal);
    const [y, m, d] = first.split('-').map(Number);
    const dow = new Date(Date.UTC(y, m - 1, d)).getUTCDay(); // 0 = Sunday
    // Jalali weeks start on Saturday, Gregorian on Monday.
    const offset = cal === 'jalali' ? (dow + 1) % 7 : (dow + 6) % 7;
    const n = monthLength(ym.y, ym.m, cal);
    return { offset, n };
  }, [ym, cal]);

  const dows = cal === 'jalali'
    ? (lang === 'fa' ? ['ش', 'ی', 'د', 'س', 'چ', 'پ', 'ج'] : ['Sa', 'Su', 'Mo', 'Tu', 'We', 'Th', 'Fr'])
    : (lang === 'fa' ? ['د', 'س', 'چ', 'پ', 'ج', 'ش', 'ی'] : ['Mo', 'Tu', 'We', 'Th', 'Fr', 'Sa', 'Su']);
  const monthLabel = monthNames
    ? `${monthNames[ym.m - 1]} ${f.digits(String(ym.y))}`
    : new Intl.DateTimeFormat(f.locale, { month: 'long', year: 'numeric', calendar: 'gregory' }).format(new Date(Date.UTC(ym.y, ym.m - 1, 1)));
  const move = (delta: number) => setYm(({ y, m }) => {
    const nm = m + delta;
    return nm < 1 ? { y: y - 1, m: 12 } : nm > 12 ? { y: y + 1, m: 1 } : { y, m: nm };
  });

  return (
    <div>
      <div className="calendar-head">
        <button type="button" className="btn btn-quiet btn-icon btn-sm" onClick={() => move(-1)} aria-label="previous"><ChevronLeft size={16} className="icon-directional" /></button>
        <span>{monthLabel}</span>
        <button type="button" className="btn btn-quiet btn-icon btn-sm" onClick={() => move(1)} aria-label="next"><ChevronRight size={16} className="icon-directional" /></button>
      </div>
      <div className="calendar-grid">
        {dows.map((d) => <span key={d} className="dow">{d}</span>)}
        {Array.from({ length: days.offset }, (_, i) => <span key={`e${i}`} />)}
        {Array.from({ length: days.n }, (_, i) => {
          const iso = isoFromCalendar(ym.y, ym.m, i + 1, cal);
          return (
            <button type="button" key={iso} className={`${iso === value ? 'selected' : ''} ${iso === today ? 'today' : ''}`} onClick={() => onPick(iso)}>
              {f.digits(String(i + 1))}
            </button>
          );
        })}
      </div>
    </div>
  );
}
