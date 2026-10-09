import { h, icon } from '../dom';

export type Speed = 'instant' | 'normal' | 'human';

// How long the pointer travels, pauses before a click and takes per typed character.
// "human" is the default for HUMAN_UI: every move has a visible duration. "instant" is for debugging/tests.
const PROFILES: Record<Speed, { move: number; pause: number; perChar: number }> = {
  instant: { move: 0, pause: 0, perChar: 0 },
  normal: { move: 350, pause: 120, perChar: 20 },
  human: { move: 800, pause: 320, perChar: 70 },
};

/** A visible virtual cursor drawn inside the page (never the OS mouse). */
export class Cursor {
  el: HTMLElement;
  label: HTMLElement;
  hl: HTMLElement;
  speed: Speed = 'human';
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

  /** The pointer is the product's visible feature, so it ignores the OS "reduce motion" setting; use speed: 'instant' to skip animation. */
  private reduced() {
    return this.speed === 'instant';
  }

  profile() {
    return PROFILES[this.speed] ?? PROFILES.human;
  }

  /** Glides to (x, y). Resolves when the movement has finished. */
  move(x: number, y: number, ms?: number): Promise<void> {
    const d = this.reduced() ? 0 : (ms ?? this.profile().move);
    this.x = x;
    this.y = y;
    this.el.style.transitionDuration = `${d}ms`;
    this.el.style.transform = `translate(${x}px, ${y}px)`;
    return new Promise((r) => setTimeout(r, d + (d ? 40 : 0)));
  }

  /**
   * Moves the pointer onto a rendered element. Accepts a resolver so the position is taken
   * from the element's CURRENT location: after scrolling, a re-render or a layout shift the
   * target is looked up again instead of trusting stale coordinates.
   */
  async to(target: Element | (() => Element | null), text?: string): Promise<Element> {
    const get = () => (typeof target === 'function' ? target() : target);
    let el = get();
    if (!el) throw new Error('target is gone');
    el.scrollIntoView({ block: 'center', inline: 'nearest', behavior: this.reduced() ? 'auto' : 'smooth' });
    // wait until the element stops moving (smooth scroll, animations, async layout)
    let last = '';
    for (let i = 0; i < 20; i++) {
      await new Promise((r) => setTimeout(r, 40));
      el = get() ?? el;
      const b = el.getBoundingClientRect();
      const key = `${Math.round(b.left)}:${Math.round(b.top)}:${Math.round(b.width)}:${Math.round(b.height)}`;
      if (key === last) break;
      last = key;
    }
    const r = el.getBoundingClientRect();
    this.highlight(r);
    if (text !== undefined) this.label.textContent = text;
    // aim at the start side of the control (right in RTL)
    const rtl = getComputedStyle(el).direction === 'rtl';
    const dx = Math.min(24, r.width / 2);
    await this.move(rtl ? r.right - dx : r.left + dx, r.top + r.height / 2);
    // the page may have shifted while the pointer travelled: follow the element
    const after = (get() ?? el).getBoundingClientRect();
    if (Math.abs(after.top - r.top) > 2 || Math.abs(after.left - r.left) > 2) {
      this.highlight(after);
      await this.move(rtl ? after.right - dx : after.left + dx, after.top + after.height / 2, 120);
    }
    return get() ?? el;
  }

  highlight(r: DOMRect) {
    this.hl.hidden = false;
    Object.assign(this.hl.style, { left: `${r.left - 3}px`, top: `${r.top - 3}px`, width: `${r.width + 6}px`, height: `${r.height + 6}px` });
  }

  async click() {
    await new Promise((r) => setTimeout(r, this.profile().pause));
    this.el.classList.remove('click');
    void this.el.offsetWidth;
    this.el.classList.add('click');
    await new Promise((r) => setTimeout(r, this.reduced() ? 0 : 260));
  }
}
