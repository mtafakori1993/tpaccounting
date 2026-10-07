import type { KeyboardEvent, RefObject } from 'react';

/** Focuses the next input/select after `from` inside `root`. Returns false at the end. */
export function focusNext(from: HTMLElement, root: HTMLElement) {
  const all = Array.from(root.querySelectorAll<HTMLElement>('input:not([disabled]):not([type=hidden]), select:not([disabled])'));
  const i = all.indexOf(from);
  if (i >= 0 && i < all.length - 1) {
    all[i + 1].focus();
    return true;
  }
  return false;
}

/** Grid keyboard handler: Enter moves to the next field; at the end calls onEnd. */
export function gridEnter(ref: RefObject<HTMLElement | null>, onEnd: () => void) {
  return (e: KeyboardEvent<HTMLElement>) => {
    const root = ref.current;
    const target = e.target as HTMLElement;
    if (!root || e.key !== 'Enter' || e.ctrlKey || e.metaKey || e.shiftKey || target.tagName === 'BUTTON' || target.tagName === 'TEXTAREA') return;
    e.preventDefault();
    if (!focusNext(target, root)) onEnd();
  };
}
