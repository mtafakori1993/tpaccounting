// Tafakor AI tags: language-independent data-ai-* attributes derived from props.
// Harmless when the AI add-on is absent (plain data attributes).
import { Children, isValidElement, type ReactNode } from 'react';
import { aiKeyOf } from '@/i18n/I18nProvider';

/** The i18n key behind a label (string or the first string child), if any. */
export function aiKey(node: ReactNode): string | undefined {
  if (typeof node === 'string') return aiKeyOf(node);
  if (typeof node === 'number' || node === null || node === undefined || typeof node === 'boolean') return undefined;
  if (Array.isArray(node)) {
    for (const n of node) {
      const k = aiKey(n);
      if (k) return k;
    }
    return undefined;
  }
  if (isValidElement<{ children?: ReactNode }>(node)) {
    for (const c of Children.toArray(node.props.children)) {
      const k = aiKey(c);
      if (k) return k;
    }
  }
  return undefined;
}

/** Route pattern of a path: numeric segments become ':id'. */
export function aiPage(pathname: string): string {
  const p = pathname.replace(/^\/+|\/+$/g, '').split('/').map((s) => (/^\d+$/.test(s) ? ':id' : s)).join('/');
  return p || 'dashboard';
}

const INTERACTIVE = 'button, a[href], input, select, textarea, [role="button"], [role="tab"], [role="menuitem"], [role="combobox"]';

function tagOne(el: Element) {
  if (el.closest('#tafakor-ai-host') || el.hasAttribute('data-ai-action') || el.hasAttribute('data-ai-nav') || el.closest('[data-ai-field]') ||
    el.hasAttribute('data-ai-tab') || el.hasAttribute('data-ai-option-id')) return;
  const text = (el.getAttribute('aria-label') || el.getAttribute('title') || (el as HTMLElement).innerText || '').trim();
  const placeholder = el.getAttribute('placeholder') ?? '';
  const key = (text && aiKeyOf(text)) || (placeholder && aiKeyOf(placeholder)) || undefined;
  if (el.matches('input, select, textarea, [role="combobox"]')) {
    if (key) el.setAttribute('data-ai-field', key);
    return;
  }
  if (el.matches('a[href]')) {
    const href = el.getAttribute('href') ?? '';
    if (href.startsWith('/')) el.setAttribute('data-ai-nav', '/' + aiPage(href.split('?')[0]));
    if (key) el.setAttribute('data-ai-action', key);
    return;
  }
  if (key) el.setAttribute('data-ai-action', key);
}

/**
 * Tags interactive elements that are not rendered by the shared components
 * (plain <button>/<a>/<input> in feature pages) from their translated label.
 * Returns a disconnect function.
 */
export function startAiTagger(root: HTMLElement = document.body): () => void {
  let queued = false;
  const run = () => {
    queued = false;
    root.querySelectorAll(INTERACTIVE).forEach(tagOne);
  };
  const mo = new MutationObserver(() => {
    if (!queued) {
      queued = true;
      requestAnimationFrame(run);
    }
  });
  mo.observe(root, { childList: true, subtree: true });
  run();
  return () => mo.disconnect();
}
