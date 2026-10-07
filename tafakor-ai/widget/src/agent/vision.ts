// Vision fallback: only when tags and accessibility lookups fail. The page is
// rendered to an image (SVG foreignObject → canvas, which keeps the Vazir font and
// RTL layout because it re-uses the live DOM and stylesheets), numbered boxes are
// drawn over the candidate elements ("set of marks"), and the server-side model
// answers with one candidate number — never raw coordinates.

import * as api from '../api';
import { elementOf, type Node } from './observer';
import type { UIStep } from './executor';

const MAX_W = 1280;

function inlineStyles(): string {
  let css = '';
  for (const sheet of Array.from(document.styleSheets)) {
    try {
      for (const r of Array.from(sheet.cssRules)) css += r.cssText + '\n';
    } catch {
      /* cross-origin sheet */
    }
  }
  return css;
}

/** Renders the visible viewport to a JPEG data URL with numbered marks over candidates. */
export async function screenshot(marks: { n: number; rect: DOMRect }[] = []): Promise<string | null> {
  const w = Math.min(window.innerWidth, MAX_W);
  const scale = w / window.innerWidth;
  const h = Math.round(window.innerHeight * scale);
  const clone = document.documentElement.cloneNode(true) as HTMLElement;
  clone.querySelectorAll('script, #tafakor-ai-host').forEach((n) => n.remove());
  // copy live form values into the clone
  const live = document.querySelectorAll('input, select, textarea');
  clone.querySelectorAll('input, select, textarea').forEach((el, i) => {
    const src = live[i] as HTMLInputElement | undefined;
    if (src) el.setAttribute('value', src.value);
  });
  const style = document.createElement('style');
  style.textContent = inlineStyles();
  clone.querySelector('head')?.append(style);
  const xhtml = new XMLSerializer().serializeToString(clone);
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="${window.innerWidth}" height="${window.innerHeight}">
    <foreignObject x="0" y="${-window.scrollY}" width="100%" height="${document.documentElement.scrollHeight}">${xhtml}</foreignObject></svg>`;
  const img = new Image();
  img.decoding = 'sync';
  const url = URL.createObjectURL(new Blob([svg], { type: 'image/svg+xml;charset=utf-8' }));
  try {
    await new Promise<void>((res, rej) => {
      img.onload = () => res();
      img.onerror = () => rej(new Error('render failed'));
      img.src = url;
    });
    const canvas = document.createElement('canvas');
    canvas.width = w;
    canvas.height = h;
    const ctx = canvas.getContext('2d')!;
    ctx.fillStyle = getComputedStyle(document.body).backgroundColor || '#fff';
    ctx.fillRect(0, 0, w, h);
    ctx.drawImage(img, 0, 0, w, h);
    ctx.font = 'bold 12px sans-serif';
    for (const m of marks) {
      const r = m.rect;
      ctx.strokeStyle = '#e11d48';
      ctx.lineWidth = 2;
      ctx.strokeRect(r.left * scale, r.top * scale, r.width * scale, r.height * scale);
      ctx.fillStyle = '#e11d48';
      ctx.fillRect(r.left * scale, r.top * scale - 14, 20, 14);
      ctx.fillStyle = '#fff';
      ctx.fillText(String(m.n), r.left * scale + 3, r.top * scale - 3);
    }
    return canvas.toDataURL('image/jpeg', 0.7);
  } catch {
    return null; // tainted canvas or unsupported: no vision, the step fails safely
  } finally {
    URL.revokeObjectURL(url);
  }
}

export async function locateByVision(step: UIStep, snap: { page: string; dialog?: string; nodes: Node[] }): Promise<HTMLElement | null> {
  const cands = snap.nodes.filter((n) => !n.disabled).slice(0, 40);
  if (!cands.length) return null;
  const marks = cands.map((c, i) => ({ n: i + 1, rect: (elementOf(c.ref) as HTMLElement).getBoundingClientRect() }));
  const image = await screenshot(marks);
  if (!image) return null;
  try {
    const res = await api.send<{ index: number }>('POST', '/ui/locate', {
      goal: { op: step.op, target: step.target, dialog: step.dialog, label: step.label },
      candidates: cands.map((c, i) => ({ n: i + 1, role: c.role, name: c.name.slice(0, 60), field: c.field, action: c.action })),
      image,
    });
    if (!res.index || res.index < 1 || res.index > cands.length) return null;
    return (elementOf(cands[res.index - 1].ref) as HTMLElement) ?? null;
  } catch {
    return null;
  }
}
