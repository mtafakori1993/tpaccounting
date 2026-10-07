// Tiny DOM helper. All text goes through textContent — never innerHTML with data.
type Attrs = Record<string, string | number | boolean | null | undefined | EventListener>;
type Child = Node | string | null | undefined | false;

export function h<K extends keyof HTMLElementTagNameMap>(tag: K, attrs: Attrs = {}, ...children: (Child | Child[])[]): HTMLElementTagNameMap[K] {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v === null || v === undefined || v === false) continue;
    if (k.startsWith('on') && typeof v === 'function') el.addEventListener(k.slice(2).toLowerCase(), v as EventListener);
    else if (k === 'class') el.className = String(v);
    else if (v === true) el.setAttribute(k, '');
    else el.setAttribute(k, String(v));
  }
  for (const c of children.flat()) {
    if (c === null || c === undefined || c === false) continue;
    el.append(typeof c === 'string' ? document.createTextNode(c) : c);
  }
  return el;
}

/** Inline SVG icons (static markup we author, never data). */
const paths: Record<string, string> = {
  spark: 'M12 2l1.9 5.6L19.5 9l-5.6 1.9L12 16.5l-1.9-5.6L4.5 9l5.6-1.4z M19 15l.9 2.1L22 18l-2.1.9L19 21l-.9-2.1L16 18l2.1-.9z',
  close: 'M6 6l12 12M18 6L6 18',
  clip: 'M21 11.5l-8.6 8.6a5 5 0 01-7-7l9.2-9.2a3.3 3.3 0 014.7 4.7l-9.2 9.2a1.7 1.7 0 01-2.4-2.4l8.6-8.6',
  mic: 'M12 3a3 3 0 00-3 3v6a3 3 0 006 0V6a3 3 0 00-3-3z M5 11a7 7 0 0014 0 M12 18v3',
  send: 'M4 12l16-8-6 16-2.5-6.5z',
  min: 'M5 12h14',
  cursor: 'M4 3l15 7-6.5 1.8L10.7 18z',
};

export function icon(name: string): SVGSVGElement {
  const ns = 'http://www.w3.org/2000/svg';
  const svg = document.createElementNS(ns, 'svg');
  svg.setAttribute('viewBox', '0 0 24 24');
  svg.setAttribute('fill', 'none');
  svg.setAttribute('stroke', 'currentColor');
  svg.setAttribute('stroke-width', '1.8');
  svg.setAttribute('stroke-linecap', 'round');
  svg.setAttribute('stroke-linejoin', 'round');
  svg.setAttribute('aria-hidden', 'true');
  const p = document.createElementNS(ns, 'path');
  p.setAttribute('d', paths[name] ?? '');
  if (name === 'spark' || name === 'cursor') {
    p.setAttribute('fill', 'currentColor');
    p.setAttribute('stroke', name === 'cursor' ? '#fff' : 'none');
  }
  svg.append(p);
  return svg;
}

export function store(key: string, value?: unknown): any {
  try {
    if (value === undefined) {
      const v = localStorage.getItem('tafakor-ai.' + key);
      return v ? JSON.parse(v) : undefined;
    }
    localStorage.setItem('tafakor-ai.' + key, JSON.stringify(value));
  } catch {
    /* storage unavailable */
  }
  return undefined;
}

export function session(key: string, value?: unknown): any {
  try {
    if (value === undefined) {
      const v = sessionStorage.getItem('tafakor-ai.' + key);
      return v ? JSON.parse(v) : undefined;
    }
    if (value === null) sessionStorage.removeItem('tafakor-ai.' + key);
    else sessionStorage.setItem('tafakor-ai.' + key, JSON.stringify(value));
  } catch {
    /* storage unavailable */
  }
  return undefined;
}
