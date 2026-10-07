import { useId, useState, type ReactNode } from 'react';
import { ChevronDown, RotateCcw, SlidersHorizontal } from 'lucide-react';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { Button } from './Button';

/**
 * Report and list filters, grouped: the few filters used most are always
 * visible; the rest sit behind "More filters" (opened automatically when one
 * of them is set). `activeCount` shows how many filters are applied and
 * enables "Clear filters".
 */
export function FilterBar({ basic, advanced, activeCount = 0, advancedActive = 0, onReset, actions, label }: {
  basic: ReactNode;
  advanced?: ReactNode;
  activeCount?: number;
  /** filters set inside the advanced group (keeps it open) */
  advancedActive?: number;
  onReset?: () => void;
  actions?: ReactNode;
  label?: string;
}) {
  const { t } = useI18n();
  const f = useFormat();
  const id = useId();
  const [open, setOpen] = useState(advancedActive > 0);
  const expanded = open || advancedActive > 0;
  return (
    <section className="filter-bar" aria-label={label ?? t('common.filters')}>
      <div className="filter-row">
        {basic}
        <div className="filter-actions">
          {advanced && (
            <Button variant="quiet" size="sm" icon={<SlidersHorizontal size={15} />} aria-expanded={expanded} aria-controls={`${id}-adv`} onClick={() => setOpen((o) => !o)}>
              {t('ui.moreFilters')}
              {advancedActive > 0 && <span className="count-pill">{f.count(advancedActive)}</span>}
              <ChevronDown size={14} className={`chev ${expanded ? 'open' : ''}`} aria-hidden />
            </Button>
          )}
          {onReset && activeCount > 0 && (
            <Button variant="quiet" size="sm" icon={<RotateCcw size={15} />} onClick={onReset}>
              {t('ui.clearFilters')} <span className="count-pill">{f.count(activeCount)}</span>
            </Button>
          )}
          {actions}
        </div>
      </div>
      {advanced && expanded && (
        <div className="filter-row advanced" id={`${id}-adv`}>
          {advanced}
        </div>
      )}
    </section>
  );
}
