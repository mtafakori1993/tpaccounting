import type { ReactNode } from 'react';
import { Check, Circle, X } from 'lucide-react';

export type StepState = 'done' | 'current' | 'upcoming' | 'skipped' | 'failed';

export interface Step {
  key: string;
  label: ReactNode;
  /** short line under the label: a date, a count, who did it */
  meta?: ReactNode;
  state: StepState;
}

/**
 * A workflow's progress: completed steps, the current one and what is left.
 * Horizontal on wide screens, vertical on narrow ones; reads in both directions.
 */
export function Stepper({ steps, label }: { steps: Step[]; label: string }) {
  return (
    <ol className="stepper" aria-label={label}>
      {steps.map((s, i) => (
        <li key={s.key} className={`step ${s.state}`} aria-current={s.state === 'current' ? 'step' : undefined}>
          <span className="step-dot" aria-hidden>
            {s.state === 'done' ? <Check size={14} strokeWidth={3} /> : s.state === 'failed' ? <X size={14} strokeWidth={3} /> : <span className="step-n">{i + 1}</span>}
          </span>
          <span className="step-text">
            <span className="step-label">{s.label}</span>
            {s.meta && <span className="step-meta">{s.meta}</span>}
          </span>
        </li>
      ))}
    </ol>
  );
}

export interface TimelineItem {
  key: string | number;
  title: ReactNode;
  /** date/time and actor */
  meta?: ReactNode;
  tone?: 'neutral' | 'success' | 'warning' | 'danger' | 'info' | 'pending';
  children?: ReactNode;
}

/** Vertical history of events (status changes, approvals, postings). */
export function Timeline({ items, label }: { items: TimelineItem[]; label: string }) {
  return (
    <ol className="timeline" aria-label={label}>
      {items.map((it) => (
        <li key={it.key} className={`tl-item tone-${it.tone ?? 'neutral'}`}>
          <span className="tl-dot" aria-hidden>{it.tone === 'pending' ? <Circle size={10} /> : null}</span>
          <div className="tl-body">
            <div className="tl-title">{it.title}</div>
            {it.meta && <div className="tl-meta">{it.meta}</div>}
            {it.children && <div className="tl-content">{it.children}</div>}
          </div>
        </li>
      ))}
    </ol>
  );
}
