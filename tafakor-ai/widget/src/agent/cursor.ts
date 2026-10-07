import { h, icon } from '../dom';

/** A visible virtual cursor drawn inside the page (never the OS mouse). */
export class Cursor {
  el: HTMLElement;
  label: HTMLElement;
  hl: HTMLElement;
  x = window.innerWidth - 80;
  y = window.innerHeight - 80;

  constructor(root: ShadowRoot) {
    this.label = h('span', { class: 'label' });
    this.el = h('div', { class: 'cursor', hidden: true, 'aria-hidden': 'true' }, icon('cursor'), h('span', { class: 'ripple' }), this.label);
    this.hl = h('div', { class: 'highlight', hidden: true });
    root.append(this.hl, this.el);
  }

  show(text?: string) {
    this.el.hidden = false;
    if (text !== undefined) this.label.textContent = text;
    this.move(this.x, this.y, 0);
  }

  hide() {
    this.el.hidden = true;
    this.hl.hidden = true;
  }

  private reduced() {
    return window.matchMedia?.('(prefers-reduced-motion: reduce)').matches;
  }

  /** Glides to (x, y). Resolves when the movement has finished. */
  move(x: number, y: number, ms = 420): Promise<void> {
    this.x = x;
    this.y = y;
    this.el.style.transform = `translate(${x}px, ${y}px)`;
    return new Promise((r) => setTimeout(r, this.reduced() ? 0 : ms));
  }

  async to(el: Element, text?: string) {
    el.scrollIntoView({ block: 'center', inline: 'nearest', behavior: this.reduced() ? 'auto' : 'smooth' });
    await new Promise((r) => setTimeout(r, 150));
    const r = el.getBoundingClientRect();
    this.highlight(r);
    if (text !== undefined) this.label.textContent = text;
    await this.move(r.left + Math.min(24, r.width / 2), r.top + r.height / 2);
  }

  highlight(r: DOMRect) {
    this.hl.hidden = false;
    Object.assign(this.hl.style, { left: `${r.left - 3}px`, top: `${r.top - 3}px`, width: `${r.width + 6}px`, height: `${r.height + 6}px` });
  }

  async click() {
    this.el.classList.remove('click');
    void this.el.offsetWidth;
    this.el.classList.add('click');
    await new Promise((r) => setTimeout(r, this.reduced() ? 0 : 220));
  }
}
