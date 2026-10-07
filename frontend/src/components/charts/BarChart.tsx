import { useState } from 'react';

export interface BarSeries { key: string; label: string; className: string }
export interface BarGroup {
  key: string;
  label: string;
  /** values per series key, as decimal strings (formatted by `format`, scaled for geometry only) */
  values: Record<string, string>;
  /** series keys drawn with the "over" style (e.g. actual above budget) */
  over?: string[];
}

/**
 * Grouped vertical bar chart with a legend and an accessible hover/focus
 * tooltip. Amounts stay strings; numbers are used only to size the bars.
 */
export function BarChart({ groups, series, format, onSelect, overLabel, height = 200 }: {
  groups: BarGroup[];
  series: BarSeries[];
  format: (v: string) => string;
  onSelect?: (g: BarGroup) => void;
  overLabel?: string;
  height?: number;
}) {
  const [hover, setHover] = useState<string | null>(null);
  const max = Math.max(1, ...groups.flatMap((g) => series.map((s) => Math.abs(Number(g.values[s.key] ?? 0)))));
  return (
    <div className="stack-sm">
      <div className="chart-legend" aria-hidden>
        {series.map((s) => <span key={s.key} className="key"><span className={`swatch s-${s.className}`} />{s.label}</span>)}
        {overLabel && <span className="key"><span className="swatch s-over" />{overLabel}</span>}
      </div>
      <div className="bars" style={{ height }} role="list">
        {groups.map((g) => (
          <div key={g.key} className="bar-group" role="listitem" tabIndex={0}
            aria-label={`${g.label}: ${series.map((s) => `${s.label} ${format(g.values[s.key] ?? '0')}`).join('، ')}`}
            onMouseEnter={() => setHover(g.key)} onMouseLeave={() => setHover(null)} onFocus={() => setHover(g.key)} onBlur={() => setHover(null)}
            onClick={onSelect ? () => onSelect(g) : undefined} onKeyDown={onSelect ? (e) => e.key === 'Enter' && onSelect(g) : undefined}
            style={{ cursor: onSelect ? 'pointer' : 'default' }}>
            {series.map((s) => {
              const v = Math.abs(Number(g.values[s.key] ?? 0));
              return <span key={s.key} className={`bar ${g.over?.includes(s.key) ? 'over' : s.className}`} style={{ height: `${(v / max) * 100}%` }} />;
            })}
            {hover === g.key && (
              <span className="bar-tip">
                <strong>{g.label}</strong>
                {series.map((s) => <span key={s.key} style={{ display: 'block' }}>{s.label}: <span className="num">{format(g.values[s.key] ?? '0')}</span></span>)}
              </span>
            )}
          </div>
        ))}
      </div>
      <div className="bar-labels" aria-hidden>{groups.map((g) => <span key={g.key}>{g.label}</span>)}</div>
    </div>
  );
}

/** Horizontal utilization meter; over 100% turns into the "over" colour. */
export function Meter({ percent, format }: { percent: string | null; format: (v: string) => string }) {
  if (percent === null) return <span className="muted">—</span>;
  const p = Number(percent);
  return (
    <span className="util">
      <span className={`meter grow ${p > 100 ? 'over' : ''}`}><span style={{ width: `${Math.min(100, Math.max(0, p))}%` }} /></span>
      <span className={`num ${p > 100 ? 'danger-text' : ''}`}>{format(percent)}</span>
    </span>
  );
}
