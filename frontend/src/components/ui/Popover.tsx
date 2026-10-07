import { useLayoutEffect, useState, type CSSProperties, type ReactNode, type RefObject } from 'react';
import { createPortal } from 'react-dom';

interface Props {
  anchor: RefObject<HTMLElement | null>;
  open: boolean;
  children: ReactNode;
  /** align to the anchor's inline start or end edge */
  align?: 'start' | 'end';
  /** at least as wide as the anchor */
  matchWidth?: boolean;
  className?: string;
  role?: string;
  id?: string;
  popoverRef?: RefObject<HTMLDivElement | null>;
  onKeyDown?: (e: React.KeyboardEvent<HTMLDivElement>) => void;
}

const GAP = 4;
const MAX_HEIGHT = 320;

/**
 * Floating panel rendered into <body> and positioned next to its anchor, so it
 * is never clipped by scrolling containers such as tables or dialogs. Flips
 * above the anchor when there is not enough room below.
 */
export function Popover({ anchor, open, children, align = 'start', matchWidth, className = '', role, id, popoverRef, onKeyDown }: Props) {
  const [style, setStyle] = useState<CSSProperties | null>(null);

  useLayoutEffect(() => {
    if (!open) return;
    const place = () => {
      const el = anchor.current;
      if (!el) return;
      const r = el.getBoundingClientRect();
      const rtl = getComputedStyle(el).direction === 'rtl';
      const vw = document.documentElement.clientWidth;
      const vh = window.innerHeight;
      const below = vh - r.bottom;
      const up = below < Math.min(MAX_HEIGHT, 200) && r.top > below;
      const s: CSSProperties = { minWidth: matchWidth ? r.width : undefined, maxHeight: Math.max(120, Math.min(MAX_HEIGHT, (up ? r.top : below) - GAP * 3)) };
      if (up) s.bottom = vh - r.top + GAP;
      else s.top = r.bottom + GAP;
      // "start" is the left edge in LTR and the right edge in RTL.
      const alignLeft = (align === 'start') !== rtl;
      if (alignLeft) s.left = Math.max(GAP, r.left);
      else s.right = Math.max(GAP, vw - r.right);
      setStyle(s);
    };
    place();
    window.addEventListener('scroll', place, true);
    window.addEventListener('resize', place);
    return () => {
      window.removeEventListener('scroll', place, true);
      window.removeEventListener('resize', place);
    };
  }, [open, anchor, align, matchWidth]);

  if (!open || !style) return null;
  return createPortal(
    <div ref={popoverRef} id={id} role={role} className={`popover floating ${className}`} style={style} onKeyDown={onKeyDown}
      dir={anchor.current ? getComputedStyle(anchor.current).direction : undefined}>
      {children}
    </div>,
    document.body,
  );
}
