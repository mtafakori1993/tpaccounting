import css from './styles.css';
import * as api from './api';
import { h, icon, session, store } from './dom';
import { actionName, lang, num, t } from './i18n';
import { runPlan, type UIPlan } from './agent/executor';
import { Cursor } from './agent/cursor';
import { navigate } from './agent/drivers';

interface Me {
  user: { id: number; name: string; language: string };
  company: { id: number; name: string; currency: string; lock_auto: boolean };
  settings: Settings;
  can_promote_rules: boolean;
  can_lock_company: boolean;
  features: { speech: boolean; tts: boolean };
}
interface Settings {
  auto_mode: boolean;
  max_amount: Record<string, string>;
  max_per_day: number;
  auto_actions: string[];
  simulation: boolean;
  voice_reply: boolean;
}
interface Pending {
  id: string;
  name: string;
}

let rootHost: HTMLElement | null = null;

export function mount() {
  if (document.getElementById('tafakor-ai-host')) return;
  const host = h('div', { id: 'tafakor-ai-host', 'data-ai-ignore': '' });
  rootHost = host;
  document.body.appendChild(host);
  const shadow = host.attachShadow({ mode: 'open' });
  shadow.append(h('style', {}, css));
  new Widget(shadow);
}

class Widget {
  me: Me | null = null;
  tab: 'chat' | 'activity' | 'learned' | 'settings' = 'chat';
  pending: Pending[] = [];
  transcript = false;
  busy = false;
  abort: AbortController | null = null;
  askThisTime = false;
  orb!: HTMLButtonElement;
  panel!: HTMLElement;
  body!: HTMLElement;
  chatLog!: HTMLElement;
  input!: HTMLTextAreaElement;
  chips!: HTMLElement;
  modebar!: HTMLElement;
  tabsEl!: HTMLElement;
  hint!: HTMLElement;
  cursor: Cursor;
  badge = 0;

  constructor(private root: ShadowRoot) {
    this.cursor = new Cursor(root);
    this.build();
    void this.loadMe();
    new MutationObserver(() => this.rebuild()).observe(document.documentElement, { attributes: true, attributeFilter: ['lang', 'dir'] });
  }

  // ---------- layout ----------

  build() {
    const dir = lang() === 'fa' ? 'rtl' : 'ltr';
    const pos = store('orb') ?? { right: 24, bottom: 24 };
    this.orb = h('button', { class: 'orb', 'aria-label': t('open'), title: t('title'), dir }, icon('spark')) as HTMLButtonElement;
    this.place(this.orb, pos);
    this.dragOrb();
    this.orb.addEventListener('click', () => {
      if ((this.orb as any)._dragged) return;
      this.toggle();
    });

    this.modebar = h('div', { class: 'modebar' });
    this.tabsEl = h('div', { class: 'tabs', role: 'tablist' });
    this.body = h('div', { class: 'body', 'aria-live': 'polite' });
    this.chatLog = h('div', { class: 'list' });
    this.input = h('textarea', { rows: 1, placeholder: t('placeholder'), 'aria-label': t('emptyHint'), dir: 'auto' }) as HTMLTextAreaElement;
    this.input.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault();
        void this.submit();
      }
    });
    this.input.addEventListener('input', () => this.autosize());
    this.input.addEventListener('paste', (e) => {
      const files = Array.from(e.clipboardData?.files ?? []);
      if (files.length) {
        e.preventDefault();
        files.forEach((f) => void this.addFile(f));
      }
    });
    this.chips = h('div', { class: 'chips' });
    this.hint = h('div', { class: 'hint' });
    const fileInput = h('input', { type: 'file', accept: 'image/jpeg,image/png,image/webp,application/pdf', multiple: true, hidden: true }) as HTMLInputElement;
    fileInput.addEventListener('change', () => {
      Array.from(fileInput.files ?? []).forEach((f) => void this.addFile(f));
      fileInput.value = '';
    });
    const attachBtn = h('button', { class: 'icon', 'aria-label': t('attach'), title: t('attach'), onclick: () => fileInput.click() }, icon('clip'));
    const micBtn = h('button', { class: 'icon', 'aria-label': t('mic'), title: t('mic') }, icon('mic'));
    this.voice(micBtn);
    const sendBtn = h('button', { class: 'icon send', 'aria-label': t('send'), title: t('send'), onclick: () => void this.submit() }, icon('send'));
    const composer = h('div', { class: 'composer' }, this.chips, this.hint, h('div', { class: 'row' }, attachBtn, micBtn, this.input, sendBtn), fileInput);

    const head = h('div', { class: 'head' }, icon('spark'), h('span', { class: 'title' }, t('title')),
      h('button', { 'aria-label': t('newTask'), title: t('newTask'), onclick: () => this.newTask() }, '＋'),
      h('button', { 'aria-label': t('close'), title: t('close'), onclick: () => this.toggle(false) }, icon('close')));
    this.panel = h('section', { class: 'panel', role: 'dialog', 'aria-label': t('title'), dir, lang: lang(), hidden: true },
      head, this.modebar, this.tabsEl, this.body, composer);
    this.place(this.panel, store('panel') ?? { right: 24, bottom: 88 });
    this.dragPanel(head);
    this.dropZone();
    this.root.append(this.orb, this.panel);
    this.renderTabs();
    this.renderMode();
    this.show();
    if (store('open')) this.toggle(true);
  }

  /** Grows the textarea with its content (placeholder never counts); scrolls only past the max height. */
  autosize() {
    const el = this.input;
    if (!el.value) {
      el.style.height = '';
      el.style.overflowY = 'hidden';
      return;
    }
    el.style.height = '0px';
    const max = 140;
    const hgt = Math.max(34, Math.min(max, el.scrollHeight));
    el.style.height = hgt + 'px';
    el.style.overflowY = el.scrollHeight > max ? 'auto' : 'hidden';
  }

  emptyState() {
    return h('div', { class: 'empty' }, h('div', {}, t('emptyHint')),
      h('div', { class: 'examples' }, ...t('examples').split('|').map((ex) =>
        h('button', { onclick: () => { this.input.value = ex; this.autosize(); this.input.focus(); } }, ex))));
  }

  rebuild() {
    const open = !this.panel.hidden;
    const log = this.chatLog;
    this.orb.remove();
    this.panel.remove();
    this.build();
    this.chatLog = log;
    this.show();
    this.toggle(open);
  }

  place(el: HTMLElement, p: { right: number; bottom: number }) {
    const rtl = lang() === 'fa';
    el.style.bottom = `${Math.max(8, p.bottom)}px`;
    el.style[rtl ? 'left' : 'right'] = `${Math.max(8, p.right)}px`;
  }

  dragOrb() {
    let sx = 0, sy = 0, start: any = null, moved = false;
    this.orb.addEventListener('pointerdown', (e) => {
      sx = e.clientX; sy = e.clientY; moved = false;
      start = store('orb') ?? { right: 24, bottom: 24 };
      this.orb.setPointerCapture(e.pointerId);
    });
    this.orb.addEventListener('pointermove', (e) => {
      if (!start) return;
      const dx = e.clientX - sx, dy = e.clientY - sy;
      if (Math.abs(dx) + Math.abs(dy) > 5) moved = true;
      if (!moved) return;
      const rtl = lang() === 'fa';
      const p = { right: Math.min(window.innerWidth - 60, start.right + (rtl ? dx : -dx)), bottom: Math.min(window.innerHeight - 60, start.bottom - dy) };
      this.place(this.orb, p);
      (this.orb as any)._pos = p;
    });
    this.orb.addEventListener('pointerup', () => {
      (this.orb as any)._dragged = moved;
      if (moved && (this.orb as any)._pos) store('orb', (this.orb as any)._pos);
      start = null;
      setTimeout(() => ((this.orb as any)._dragged = false), 0);
    });
  }

  dragPanel(handle: HTMLElement) {
    let sx = 0, sy = 0, start: any = null;
    handle.addEventListener('pointerdown', (e) => {
      if ((e.target as HTMLElement).closest('button') || window.innerWidth <= 560) return;
      sx = e.clientX; sy = e.clientY;
      start = store('panel') ?? { right: 24, bottom: 88 };
      handle.setPointerCapture(e.pointerId);
    });
    handle.addEventListener('pointermove', (e) => {
      if (!start) return;
      const rtl = lang() === 'fa';
      const dx = e.clientX - sx, dy = e.clientY - sy;
      const p = { right: Math.max(8, start.right + (rtl ? dx : -dx)), bottom: Math.max(8, start.bottom - dy) };
      this.place(this.panel, p);
      (handle as any)._pos = p;
    });
    handle.addEventListener('pointerup', () => {
      if (start && (handle as any)._pos) store('panel', (handle as any)._pos);
      start = null;
    });
  }

  dropZone() {
    let overlay: HTMLElement | null = null;
    const hide = () => { overlay?.remove(); overlay = null; };
    this.panel.addEventListener('dragover', (e) => {
      e.preventDefault();
      if (!overlay) {
        overlay = h('div', { class: 'drop' }, t('attach'));
        this.panel.append(overlay);
      }
    });
    this.panel.addEventListener('dragleave', (e) => { if (e.target === this.panel) hide(); });
    this.panel.addEventListener('drop', (e) => {
      e.preventDefault();
      hide();
      Array.from(e.dataTransfer?.files ?? []).forEach((f) => void this.addFile(f));
    });
  }

  toggle(open?: boolean) {
    const o = open ?? this.panel.hidden;
    this.panel.hidden = !o;
    store('open', o);
    if (o) {
      this.badge = 0;
      this.renderBadge();
      setTimeout(() => this.input.focus(), 0);
    }
  }

  renderBadge() {
    this.orb.querySelector('.badge')?.remove();
    if (this.badge > 0) this.orb.append(h('span', { class: 'badge' }, String(this.badge)));
  }

  renderTabs() {
    this.tabsEl.replaceChildren(
      ...(['chat', 'activity', 'learned', 'settings'] as const).map((k) =>
        h('button', { role: 'tab', 'aria-selected': String(this.tab === k), onclick: () => { this.tab = k; this.renderTabs(); this.show(); } }, t(k))),
    );
  }

  show() {
    if (this.tab === 'chat') {
      this.body.replaceChildren(this.chatLog.childElementCount ? this.chatLog : this.emptyState());
    } else if (this.tab === 'activity') {
      void this.showActivity();
    } else if (this.tab === 'learned') {
      void this.showLearned();
    } else {
      this.showSettings();
    }
  }

  renderMode() {
    const auto = !!this.me?.settings.auto_mode;
    const sw = h('button', { class: 'switch', role: 'switch', 'aria-checked': String(auto), 'aria-label': t('autoMode') }) as HTMLButtonElement;
    sw.addEventListener('click', () => void this.setAuto(!auto));
    const ask = h('input', { type: 'checkbox' }) as HTMLInputElement;
    ask.checked = this.askThisTime;
    ask.addEventListener('change', () => (this.askThisTime = ask.checked));
    this.modebar.className = 'modebar' + (auto ? ' auto' : '');
    this.modebar.replaceChildren(
      h('span', { class: 'dot' }),
      h('span', {}, auto ? t('modeAuto') : t('modeAsk')),
      auto ? h('label', { class: 'inline', title: t('askThisTime') }, ask, t('askThisTime')) : '',
      h('label', {}, h('span', {}, t('autoMode')), sw),
    );
  }

  async setAuto(on: boolean) {
    if (on) {
      if (this.me?.company.lock_auto) return this.toast(t('autoLocked'));
      if (!window.confirm(t('autoRisk'))) return; // explicit risk confirmation, every time it is turned on
    }
    try {
      const s = await api.send<Settings>('PUT', '/settings', { auto_mode: on, risk_confirmed: on });
      if (this.me) this.me.settings = s;
      this.renderMode();
    } catch (e: any) {
      this.toast(e.message);
    }
  }

  toast(text: string) {
    const el = h('div', { class: 'toast', role: 'status', dir: lang() === 'fa' ? 'rtl' : 'ltr' }, text);
    this.root.append(el);
    setTimeout(() => el.remove(), 4000);
  }

  async loadMe() {
    try {
      this.me = await api.get<Me>('/me');
    } catch (e: any) {
      if (e.status === 401) {
        this.orb.hidden = true; // not signed in: stay invisible (login page)
        setTimeout(() => void this.loadMe().then(() => (this.orb.hidden = !this.me)), 5000);
      }
      return;
    }
    this.orb.hidden = false;
    this.renderMode();
    void this.resume();
  }

  // ---------- composer ----------

  async addFile(f: File) {
    if (!['image/jpeg', 'image/png', 'image/webp', 'application/pdf'].includes(f.type)) return this.toast(t('attach') + ': JPG/PNG/WEBP/PDF');
    const chip = h('span', { class: 'chip' }, f.name + ' …');
    this.chips.append(chip);
    try {
      const up = await api.upload(f, f.name || 'pasted.png');
      this.pending.push({ id: up.id, name: up.name });
      chip.replaceChildren(f.name || 'image', h('button', { 'aria-label': t('remove'), onclick: () => { this.pending = this.pending.filter((p) => p.id !== up.id); chip.remove(); } }, '×'));
    } catch (e: any) {
      chip.remove();
      this.toast(e.message);
    }
  }

  voice(btn: HTMLElement) {
    let rec: MediaRecorder | null = null;
    let chunks: Blob[] = [];
    const start = async (e: Event) => {
      e.preventDefault();
      if (!this.me?.features.speech) return this.toast(lang() === 'fa' ? 'ورودی صوتی روی این سرور فعال نیست.' : 'Voice input is not configured on this server.');
      try {
        const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
        rec = new MediaRecorder(stream);
        chunks = [];
        rec.ondataavailable = (ev) => chunks.push(ev.data);
        rec.onstop = async () => {
          stream.getTracks().forEach((tr) => tr.stop());
          btn.classList.remove('rec');
          this.hint.textContent = t('transcribing');
          try {
            const res = await api.transcribe(new Blob(chunks, { type: rec?.mimeType || 'audio/webm' }), lang());
            // Always show the transcript (editable) before anything happens.
            this.input.value = res.normalized || res.text;
            this.autosize();
            this.transcript = true;
            this.hint.textContent = t('transcriptHint');
            this.input.focus();
          } catch (err: any) {
            this.hint.textContent = '';
            this.toast(err.message);
          }
        };
        rec.start();
        btn.classList.add('rec');
        this.hint.textContent = t('recording');
      } catch (err: any) {
        this.toast(err.message);
      }
    };
    const stop = () => {
      if (rec && rec.state === 'recording') rec.stop();
    };
    btn.addEventListener('pointerdown', start);
    btn.addEventListener('pointerup', stop);
    btn.addEventListener('pointerleave', stop);
    btn.addEventListener('keydown', (e) => { if ((e as KeyboardEvent).key === ' ' && !rec) void start(e); });
    btn.addEventListener('keyup', (e) => { if ((e as KeyboardEvent).key === ' ') stop(); });
  }

  newTask() {
    this.abort?.abort();
    session('task', null);
    session('conv', null); // a new conversation
    this.chatLog.replaceChildren();
    this.tab = 'chat';
    this.renderTabs();
    this.show();
  }

  queued = false;

  async submit() {
    const text = this.input.value.trim();
    if (!text && !this.pending.length) return;
    if (this.busy) {
      // never drop what the user typed: send it as soon as the current step finishes
      this.queued = true;
      this.hint.textContent = t('queued');
      return;
    }
    const uploads = this.pending.map((p) => p.id);
    this.chatLog.append(h('div', { class: 'msg user' }, text || this.pending.map((p) => p.name).join(', ')));
    this.input.value = '';
    this.autosize();
    this.chips.replaceChildren();
    this.pending = [];
    this.hint.textContent = '';
    const transcript = this.transcript;
    this.transcript = false;
    this.tab = 'chat';
    this.renderTabs();
    this.show();
    await this.run('/tasks', { message: text, uploads, ask_this_time: this.askThisTime, exec_mode: store('exec') ?? 'ui', transcript,
      conversation_id: session('conv') ?? '' });
  }

  // ---------- task streaming ----------

  async run(path: string, body: unknown) {
    this.busy = true;
    this.orb.classList.add('busy');
    const turn = h('div', { class: 'list' });
    const statusEl = h('div', { class: 'status' }, h('span', { class: 'spinner' }), t('thinking'));
    const steps = h('div', { class: 'steps' });
    turn.append(steps, statusEl);
    this.chatLog.append(turn);
    let streamMsg: HTMLElement | null = null;
    let lastStatus = '';
    this.abort = new AbortController();
    const scroll = () => (this.body.scrollTop = this.body.scrollHeight);
    try {
      await api.stream(path, body, (ev, d) => {
        switch (ev) {
          case 'conversation':
            session('conv', d.conversation_id);
            break;
          case 'task':
            session('task', d.task_id);
            session('lastTask', d.task_id);
            break;
          case 'status':
            if (lastStatus && lastStatus !== d.text) steps.append(h('div', { class: 'ok' }, lastStatus));
            lastStatus = d.text;
            statusEl.replaceChildren(h('span', { class: 'spinner' }), d.text);
            break;
          case 'delta':
            if (!streamMsg) {
              streamMsg = h('div', { class: 'msg ai' });
              turn.insertBefore(streamMsg, statusEl);
            }
            streamMsg.textContent += d.text;
            break;
          case 'message':
            turn.insertBefore(h('div', { class: 'msg ai' }, d.text), statusEl);
            this.speak(d.text);
            break;
          case 'error':
            turn.insertBefore(h('div', { class: 'msg ai err', role: 'alert' }, d.message), statusEl);
            break;
          case 'extraction':
            turn.insertBefore(this.extractionCard(d), statusEl);
            break;
          case 'question':
            turn.insertBefore(this.questionCard(d), statusEl);
            break;
          case 'preview':
            turn.insertBefore(this.previewCard(d), statusEl);
            break;
          case 'approval':
            turn.insertBefore(this.approvalCard(d), statusEl);
            break;
          case 'result':
            turn.insertBefore(this.resultCard(d), statusEl);
            this.speak(d.summary);
            break;
          case 'offers':
            turn.insertBefore(this.offersCard(d), statusEl);
            break;
          case 'table':
            turn.insertBefore(this.tableCard(d), statusEl);
            break;
          case 'file':
            turn.insertBefore(h('div', { class: 'card' }, h('a', { href: d.url, target: '_blank', rel: 'noopener', class: 'btn primary' }, 'PDF ⬇')), statusEl);
            break;
          case 'navigate':
            void navigate(d.route);
            break;
          case 'notify':
            this.toast(d.text);
            if (this.panel.hidden) { this.badge++; this.renderBadge(); }
            break;
          case 'ui_plan':
            queueMicrotask(() => void this.runUI(d));
            break;
          case 'ui_failed':
            turn.insertBefore(this.uiFailedCard(d), statusEl);
            break;
          case 'done':
            if (d.stage !== 'ui_running' && d.stage !== 'awaiting_answer' && d.stage !== 'awaiting_approval' && d.stage !== 'ui_failed') session('task', null);
            break;
        }
        scroll();
      }, this.abort.signal);
    } catch (e: any) {
      if (e.name !== 'AbortError') turn.append(h('div', { class: 'msg ai err', role: 'alert' }, e.message || t('error')));
    } finally {
      if (lastStatus) steps.append(h('div', { class: 'ok' }, lastStatus));
      statusEl.remove();
      this.busy = false;
      this.orb.classList.remove('busy');
      scroll();
      if (this.queued) {
        this.queued = false;
        this.hint.textContent = '';
        queueMicrotask(() => void this.submit());
      }
    }
  }

  speak(text: string) {
    if (!text || !this.me?.features.tts || !this.me.settings.voice_reply || !('speechSynthesis' in window)) return;
    const u = new SpeechSynthesisUtterance(text);
    u.lang = lang() === 'fa' ? 'fa-IR' : 'en-US';
    speechSynthesis.speak(u);
  }

  // ---------- cards ----------

  extractionCard(x: any) {
    const rows: [string, any][] = [[t('amount'), x.amount], [t('date'), x.date], [t('party'), x.sender?.value ? x.sender : x.merchant], [t('reference'), x.reference]];
    return h('div', { class: 'card' }, h('h4', {}, '📄 ' + (x.file_name || '')),
      h('dl', { class: 'kv' }, ...rows.filter(([, f]) => f && f.value).flatMap(([k, f]) => [
        h('dt', {}, k),
        h('dd', {}, (f.raw || f.value) + (f.confidence < 0.75 || f.valid === false ? ` ⚠ ${t('lowConfidence')}` : '')),
      ])),
      x.instructions_detected ? h('div', { class: 'reason warn' }, lang() === 'fa' ? 'متن دستورمانند داخل سند نادیده گرفته شد.' : 'Instruction-like text inside the document was ignored.') : null);
  }

  questionCard(q: any) {
    const taskId = session('task');
    const remember = h('input', { type: 'checkbox' }) as HTMLInputElement;
    const card = h('div', { class: 'card', role: 'group' }, h('h4', {}, q.text));
    const answer = async (value: string) => {
      card.querySelectorAll('button,input').forEach((b) => ((b as HTMLButtonElement).disabled = true));
      await this.run(`/tasks/${taskId}/answer`, { question_id: q.id, value, remember: remember.checked });
    };
    if (q.free_text) {
      const inp = h('input', { type: 'text', dir: 'ltr', inputmode: 'decimal' }) as HTMLInputElement;
      card.append(h('div', { class: 'form' }, inp), h('div', { class: 'actions' }, h('button', { class: 'btn primary', onclick: () => inp.value.trim() && void answer(inp.value.trim()) }, t('send'))));
    } else {
      card.append(h('div', { class: 'opts' }, ...(q.options ?? []).map((o: any) =>
        h('button', { class: 'btn' + (o.recommended ? ' rec' : ''), onclick: () => void answer(o.value) }, o.label))));
    }
    if (q.remember) card.append(h('label', { class: 'hint' }, remember, ' ', t('remember')));
    return card;
  }

  previewCard(p: any) {
    const d = p.display ?? {};
    const rows: [string, string | undefined][] = [
      [t('documentType'), d.document_type], [t('amount'), d.amount ? num(d.amount) + ' ' + (d.currency ?? '') + (d.original_amount ? ` (${d.original_amount})` : '') : undefined],
      [t('date'), lang() === 'fa' ? d.date_jalali : d.date], [t('party'), d.party], [t('project'), d.project], [t('account'), d.treasury],
      [t('counterAccount'), d.counter_account], [t('category'), d.category], [t('reference'), d.reference], [t('description'), d.description],
      [t('product'), d.product], [t('productCode'), d.product_code], [t('quantity'), d.quantity ? num(d.quantity) + (d.unit ? ' ' + d.unit : '') : undefined],
      [t('unitValue'), d.unit_value ? num(d.unit_value) + ' ' + (d.currency ?? '') : undefined], [t('warehouse'), d.warehouse],
      [t('attachment'), d.attachment],
    ];
    const card = h('div', { class: 'card' },
      h('dl', { class: 'kv' }, ...rows.filter(([, v]) => v).flatMap(([k, v]) => [h('dt', {}, k), h('dd', {}, v!)])));
    if (p.preview?.length) {
      card.append(h('table', { class: 'entry' }, h('tbody', {}, ...p.preview.map((l: any) =>
        h('tr', {}, h('td', {}, l.side === 'debit' ? t('debit') : t('credit')), h('td', {}, l.account + (l.cost_center ? ` · ${l.cost_center}` : '')), h('td', { class: 'n' }, num(l.amount)))))));
    }
    for (const n of p.notes ?? []) card.append(h('div', { class: 'hint' }, n));
    for (const m of p.duplicates ?? []) card.append(h('div', { class: 'reason warn' }, `${t('duplicateFound')} #${num(m.candidate.Number ?? m.candidate.number ?? '')} (${m.candidate.Date ?? ''}, ${num(String(m.candidate.Amount ?? ''))})`));
    if (p.decision?.simulated) card.append(h('div', { class: 'reason ok' }, t('simulated')));
    this.lastPreview = p;
    return card;
  }

  approvalCard(a: any) {
    const taskId = a.task_id ?? session('task');
    const reasons: { code: string; text: string }[] = a.reasons ?? [];
    const card = h('div', { class: 'card' },
      h('div', { class: 'reason' + (a.duplicate ? ' warn' : '') }, `${t('waiting')}: ${reasons.map((r) => r.text).join('؛ ') || '—'}`));
    const disable = () => card.querySelectorAll('button').forEach((b) => ((b as HTMLButtonElement).disabled = true));
    const exec = store('exec') ?? 'ui';
    const execSel = h('select', { 'aria-label': 'execution' },
      h('option', { value: 'ui', selected: exec === 'ui' }, t('executionUI')), h('option', { value: 'api', selected: exec === 'api' }, t('executionAPI'))) as HTMLSelectElement;
    execSel.addEventListener('change', () => store('exec', execSel.value));
    const confirmBtn = h('button', { class: 'btn primary', onclick: async () => {
      disable();
      await this.run(`/tasks/${taskId}/approve`, { token: a.token, payload_hash: a.payload_hash, exec_mode: execSel.value });
    } }, a.duplicate ? t('registerAnyway') : t('confirm'));
    const cancelBtn = h('button', { class: 'btn', onclick: async () => {
      disable();
      await api.send('POST', `/tasks/${taskId}/cancel`).catch(() => undefined);
      session('task', null);
      card.append(h('div', { class: 'hint' }, t('cancel') + ' ✓'));
    } }, t('cancel'));
    const reviewBtn = h('button', { class: 'btn', onclick: () => { card.append(this.reviewForm(taskId)); reviewBtn.setAttribute('disabled', ''); } }, t('review'));
    card.append(h('div', { class: 'actions' }, cancelBtn, reviewBtn, confirmBtn), h('div', { class: 'form' }, execSel));
    return card;
  }

  reviewForm(taskId: string) {
    const fields: [string, string, string][] = [['amount', t('amount'), 'decimal'], ['date', t('date') + ' (YYYY-MM-DD)', 'text'],
      ['reference', t('reference'), 'text'], ['description', t('description'), 'text']];
    const inputs: Record<string, HTMLInputElement> = {};
    const selects: Record<string, HTMLSelectElement> = {};
    const form = h('div', { class: 'form' });
    const action = this.lastPreview?.action ?? '';
    // id choices come from the app's own API, read as the signed-in user
    const addSelect = async (key: string, label: string, path: string, map: (x: any) => [number, string] | null) => {
      try {
        const res = await fetch('/api/v1' + path, { credentials: 'same-origin' });
        if (!res.ok) return;
        const data = await res.json();
        const items: any[] = Array.isArray(data) ? data : data.items ?? [];
        const sel = h('select', {}, h('option', { value: '' }, '—'), ...items.map(map).filter(Boolean).map((p) => h('option', { value: String(p![0]) }, p![1]))) as HTMLSelectElement;
        selects[key] = sel;
        form.prepend(h('label', {}, label, sel));
      } catch {
        /* list unavailable: the field simply isn't offered */
      }
    };
    if (action === 'create_expense') void addSelect('category_id', t('category'), '/expenses/categories', (c) => (c.is_active ? [c.id, c.name] : null));
    if (action === 'create_expense' || action === 'create_receipt' || action === 'create_payment') {
      void addSelect('treasury_account_id', t('account'), '/treasury/accounts', (a) => (a.is_active ? [a.id, a.name] : null));
      void addSelect('cost_center_id', t('project'), '/cost-centers?page_size=200', (c) => (c.is_active ? [c.id, c.name] : null));
    }
    form.append(...fields.map(([k, label, mode]) => {
      inputs[k] = h('input', { type: 'text', inputmode: mode, dir: k === 'description' ? 'auto' : 'ltr' }) as HTMLInputElement;
      return h('label', {}, label, inputs[k]);
    }));
    form.append(h('div', { class: 'actions' }, h('button', { class: 'btn primary', onclick: async () => {
      const body: Record<string, string | number> = {};
      for (const [k, el] of Object.entries(inputs)) if (el.value.trim()) body[k] = el.value.trim();
      for (const [k, el] of Object.entries(selects)) if (el.value) body[k] = Number(el.value);
      if (!Object.keys(body).length) return;
      await this.run(`/tasks/${taskId}/edit`, body);
    } }, t('saveChanges'))));
    return form;
  }

  lastPreview: any = null;

  resultCard(d: any) {
    const r = d.result ?? {};
    const card = h('div', { class: 'card' },
      h('div', { class: 'reason ' + (r.verified ? 'ok' : 'warn') }, (r.mode === 'auto' ? t('registeredAuto') + ' — ' : '') + d.summary));
    const taskId = session('task') ?? d.task_id;
    const actions = h('div', { class: 'actions' });
    if (r.route) actions.append(h('button', { class: 'btn', onclick: () => void navigate(r.route) }, t('openDoc')));
    if (r.verified && (r.entity_type === 'treasury_transaction' || r.entity_type === 'expense')) {
      const tid = this.lastTask;
      actions.append(h('button', { class: 'btn', onclick: () => void this.run(`/tasks/${tid}/pdf`, {}) }, t('pdf')));
      if (d.undo_available) actions.append(h('button', { class: 'btn danger', onclick: () => window.confirm(t('undoConfirm')) && void this.run(`/tasks/${tid}/undo`, {}) }, t('undo')));
    }
    void taskId;
    card.append(actions);
    return card;
  }

  get lastTask(): string {
    return session('lastTask') ?? session('task');
  }

  offersCard(offers: any[]) {
    const taskId = this.lastTask;
    return h('div', { class: 'card' }, h('h4', {}, t('rememberQ')), ...offers.map((o) =>
      h('div', { class: 'actions' }, h('span', { class: 'hint' }, `${o.pattern?.split(':').slice(1).join(':')} → ${o.result?.name || o.label || ''}`),
        h('button', { class: 'btn primary', onclick: async (e: Event) => {
          (e.target as HTMLButtonElement).disabled = true;
          await api.send('POST', `/tasks/${taskId}/remember`, { offer_id: o.id }).then(() => this.toast(t('saved'))).catch((er) => this.toast(er.message));
        } }, t('yes')))));
  }

  tableCard(rows: any[]) {
    if (!rows?.length) return h('div', {});
    const cols = Object.keys(rows[0]);
    return h('div', { class: 'card' }, h('table', { class: 'data' }, h('thead', {}, h('tr', {}, ...cols.map((c) => h('th', {}, c)))),
      h('tbody', {}, ...rows.slice(0, 50).map((r) => h('tr', {}, ...cols.map((c) => h('td', {}, num(String(r[c] ?? '')))))))));
  }

  uiFailedCard(d: any) {
    const taskId = d.task_id;
    return h('div', { class: 'card' }, h('div', { class: 'reason warn' }, d.message), h('div', { class: 'actions' },
      h('button', { class: 'btn', onclick: () => void this.retryUI(taskId) }, t('retry')),
      d.can_fallback ? h('button', { class: 'btn primary', onclick: () => void this.run(`/tasks/${taskId}/ui-result`, { ok: false, fallback_api: true }) }, t('uiFailedFallback')) : null));
  }

  async retryUI(taskId: string) {
    const task = await api.get<any>(`/tasks/${taskId}`);
    if (task.ui_plan) await this.runUI({ ...task.ui_plan, task_id: taskId });
  }

  // ---------- UI agent ----------

  async runUI(plan: UIPlan) {
    session('lastTask', plan.task_id);
    const statusEl = h('div', { class: 'status' }, h('span', { class: 'spinner' }), '…');
    this.chatLog.append(statusEl);
    // get out of the way while operating the page; the cursor label shows progress
    const wasOpen = !this.panel.hidden;
    this.panel.hidden = true;
    this.orb.classList.add('busy');
    const stop = h('button', { class: 'btn danger', style: 'position:fixed;z-index:2147483004;bottom:84px;' + (lang() === 'fa' ? 'left' : 'right') + ':24px',
      onclick: () => { this.abortUI = true; } }, t('cancel'));
    this.root.append(stop);
    this.abortUI = false;
    const res = await runPlan(plan, this.cursor, (label) => { statusEl.replaceChildren(h('span', { class: 'spinner' }), label); this.cursor.label.textContent = label; }, () => this.abortUI);
    stop.remove();
    statusEl.remove();
    if (wasOpen) this.toggle(true);
    await this.run(`/tasks/${plan.task_id}/ui-result`, res);
  }

  abortUI = false;

  /** After a refresh: rebuild the conversation (transcript + open question/approval) from the server. */
  async restoreConversation(): Promise<boolean> {
    const conv = session('conv');
    if (!conv) return false;
    let v: any;
    try {
      v = await api.get<any>(`/conversations/${conv}`);
    } catch {
      session('conv', null);
      return false;
    }
    const bubbles: HTMLElement[] = [];
    for (const m of v.messages ?? []) {
      if (m.role === 'user' && m.kind === 'text' && m.text) bubbles.push(h('div', { class: 'msg user' }, m.text));
      else if (m.role === 'user' && m.kind === 'answer' && m.text) bubbles.push(h('div', { class: 'msg user' }, m.text));
      else if (m.role === 'assistant' && ['text', 'error', 'result'].includes(m.kind) && m.text)
        bubbles.push(h('div', { class: 'msg ai' + (m.kind === 'error' ? ' err' : '') }, m.text));
      else if (m.role === 'assistant' && m.kind === 'question') bubbles.push(h('div', { class: 'msg ai' }, (m.data?.text as string) ?? m.text));
    }
    if (!bubbles.length) return false;
    this.chatLog.replaceChildren(...bubbles);
    const task = v.task;
    if (task) {
      session('task', task.id);
      session('lastTask', task.id);
      if (task.stage === 'awaiting_answer' && task.proposal?.question) {
        this.chatLog.lastElementChild?.remove(); // the question bubble becomes a live card
        this.chatLog.append(this.questionCard(task.proposal.question));
      } else if (task.stage === 'awaiting_approval') {
        void this.run(`/tasks/${task.id}/reissue`, {});
      } else if (task.stage === 'ui_failed') {
        this.chatLog.append(this.uiFailedCard({ task_id: task.id, message: task.message || t('stopped'), can_fallback: true }));
      } else if (task.stage === 'ui_running' && task.ui_plan) {
        const card = h('div', { class: 'card' }, h('h4', {}, t('resumeQ')), h('div', { class: 'actions' },
          h('button', { class: 'btn', onclick: async () => { await api.send('POST', `/tasks/${task.id}/cancel`).catch(() => undefined); card.remove(); } }, t('cancel')),
          h('button', { class: 'btn primary', onclick: async () => { card.remove(); await this.runUI({ ...task.ui_plan, task_id: task.id }); } }, t('resume'))));
        this.chatLog.append(card);
      }
    }
    this.show();
    return true;
  }

  async resume() {
    if (await this.restoreConversation()) return;
    const id = session('task');
    if (!id) return;
    try {
      const task = await api.get<any>(`/tasks/${id}`);
      if (!['awaiting_answer', 'awaiting_approval', 'ui_running', 'ui_failed'].includes(task.stage)) {
        session('task', null);
        return;
      }
      this.toggle(true);
      const card = h('div', { class: 'card' }, h('h4', {}, t('resumeQ')), h('div', { class: 'hint' }, task.request || ''));
      const actions = h('div', { class: 'actions' },
        h('button', { class: 'btn', onclick: async () => { await api.send('POST', `/tasks/${id}/cancel`).catch(() => undefined); session('task', null); card.remove(); } }, t('cancel')),
        h('button', { class: 'btn primary', onclick: async () => {
          card.remove();
          if (task.stage === 'ui_running' && task.ui_plan) await this.runUI({ ...task.ui_plan, task_id: id });
          else if (task.stage === 'awaiting_answer' && task.proposal?.question) this.chatLog.append(this.questionCard(task.proposal.question));
          else if (task.stage === 'ui_failed') this.chatLog.append(this.uiFailedCard({ task_id: id, message: task.message || t('stopped'), can_fallback: true }));
          else if (task.stage === 'awaiting_approval') this.chatLog.append(h('div', { class: 'msg ai' }, lang() === 'fa' ? 'تایید قبلی منقضی شده؛ لطفاً درخواست را دوباره بفرستید.' : 'The earlier approval card expired; please send the request again.'));
        } }, t('resume')));
      card.append(actions);
      this.chatLog.append(card);
    } catch {
      session('task', null);
    }
  }

  // ---------- other tabs ----------

  async showActivity() {
    const box = h('div', { class: 'list' }, h('div', { class: 'status' }, h('span', { class: 'spinner' })));
    this.body.replaceChildren(box);
    try {
      const data = await api.get<any>('/activity');
      if (!data.items.length) return box.replaceChildren(h('div', { class: 'hint' }, t('noActivity')));
      box.replaceChildren(...data.items.map((it: any) => h('div', { class: 'item' },
        h('div', {}, h('span', { class: 'tag' + (it.mode === 'auto' ? ' auto' : '') }, it.mode === 'auto' ? t('modeAuto') : t('modeAsk')), ' ', it.summary),
        h('div', { class: 'meta' }, new Date(it.when).toLocaleString(lang() === 'fa' ? 'fa-IR' : 'en-GB') + (it.reason ? ` — ${it.reason}` : '')),
        h('div', { class: 'actions' },
          it.result?.route ? h('button', { class: 'btn', onclick: () => void navigate(it.result.route) }, t('openDoc')) : null,
          it.undo_available ? h('button', { class: 'btn danger', onclick: () => {
            if (!window.confirm(t('undoConfirm'))) return;
            this.tab = 'chat'; this.renderTabs(); this.show();
            session('lastTask', it.task_id);
            void this.run(`/tasks/${it.task_id}/undo`, {});
          } }, t('undo')) : null))),
        h('div', { class: 'hint' }, `${t('cost')}: ${num(data.cost_30d.prompt_tokens + data.cost_30d.completion_tokens)} tokens · $${data.cost_30d.cost_usd.toFixed(3)}`));
    } catch (e: any) {
      box.replaceChildren(h('div', { class: 'msg ai err' }, e.message));
    }
  }

  async showLearned() {
    const box = h('div', { class: 'list' });
    this.body.replaceChildren(box);
    try {
      const data = await api.get<any>('/rules');
      if (!data.rules.length && !data.aliases.length) return box.replaceChildren(h('div', { class: 'hint' }, t('noRules')));
      const reload = () => void this.showLearned();
      box.replaceChildren(
        ...data.rules.map((r: any) => h('div', { class: 'item' },
          h('div', {}, h('b', {}, r.pattern), ' → ', r.result?.name || r.label || '#' + r.result?.id),
          h('div', { class: 'meta' }, `${r.kind} · ${r.scope === 'company' ? t('ruleCompany') : t('rulePersonal')} · ${r.stage === 'apply' ? t('applyStage') : t('suggestStage')} · ${t('confirmedTimes', { n: r.times_confirmed })} · ${r.source} · ${new Date(r.updated_at).toLocaleDateString(lang() === 'fa' ? 'fa-IR' : 'en-GB')}${r.status !== 'active' ? ' · ' + r.status : ''}`),
          h('div', { class: 'actions' },
            h('button', { class: 'btn', onclick: () => api.send('PUT', `/rules/${r.id}`, { status: r.status === 'active' ? 'disabled' : 'active' }).then(reload).catch((e) => this.toast(e.message)) }, r.status === 'active' ? t('disable') : t('enable')),
            data.can_promote ? h('button', { class: 'btn', onclick: () => api.send('PUT', `/rules/${r.id}`, { scope: r.scope === 'company' ? 'personal' : 'company' }).then(reload).catch((e) => this.toast(e.message)) }, r.scope === 'company' ? t('makePersonal') : t('makeCompany')) : null,
            h('button', { class: 'btn danger', onclick: () => api.send('DELETE', `/rules/${r.id}`).then(reload).catch((e) => this.toast(e.message)) }, t('delete'))))),
        ...data.aliases.map((a: any) => h('div', { class: 'item' }, h('div', {}, h('b', {}, a.alias), ' → ', a.target_name),
          h('div', { class: 'actions' }, h('button', { class: 'btn danger', onclick: () => api.send('DELETE', `/aliases/${a.id}`).then(reload) }, t('delete'))))),
      );
    } catch (e: any) {
      box.replaceChildren(h('div', { class: 'msg ai err' }, e.message));
    }
  }

  showSettings() {
    const s = this.me?.settings;
    if (!s) return this.body.replaceChildren(h('div', { class: 'status' }, h('span', { class: 'spinner' })));
    const cur = this.me!.company.currency || 'IRR';
    const maxAmt = h('input', { type: 'text', inputmode: 'decimal', dir: 'ltr', value: s.max_amount[cur] ?? '' }) as HTMLInputElement;
    const perDay = h('input', { type: 'number', min: 0, max: 500, value: s.max_per_day }) as HTMLInputElement;
    const actions = ['create_receipt', 'create_expense', 'create_payment'].map((a) => {
      const c = h('input', { type: 'checkbox', value: a }) as HTMLInputElement;
      c.checked = s.auto_actions.includes(a);
      return h('label', { class: 'inline' }, c, actionName(a));
    });
    const sim = h('input', { type: 'checkbox' }) as HTMLInputElement;
    sim.checked = s.simulation;
    const voice = h('input', { type: 'checkbox' }) as HTMLInputElement;
    voice.checked = s.voice_reply;
    const form = h('div', { class: 'form' }, h('h4', {}, t('limits')),
      h('label', {}, t('maxAmount', { cur }), maxAmt), h('label', {}, t('maxPerDay'), perDay),
      h('div', {}, t('autoActions')), ...actions,
      h('label', { class: 'inline' }, sim, t('simulation')),
      this.me!.features.tts ? h('label', { class: 'inline' }, voice, t('voiceReply')) : null,
      h('div', { class: 'actions' }, h('button', { class: 'btn primary', onclick: async () => {
        try {
          const body = {
            max_amount: { ...s.max_amount, [cur]: maxAmt.value.replace(/[,٬\s]/g, '') || '0' },
            max_per_day: Number(perDay.value) || 0,
            auto_actions: actions.map((l) => l.querySelector('input') as HTMLInputElement).filter((c) => c.checked).map((c) => c.value),
            simulation: sim.checked, voice_reply: voice.checked,
          };
          this.me!.settings = await api.send<Settings>('PUT', '/settings', body);
          this.toast(t('saved'));
        } catch (e: any) {
          this.toast(e.message);
        }
      } }, t('saveChanges'))));
    if (this.me!.can_lock_company) {
      const lock = h('input', { type: 'checkbox' }) as HTMLInputElement;
      lock.checked = this.me!.company.lock_auto;
      lock.addEventListener('change', () => api.send('PUT', '/company-settings', { lock_auto: lock.checked }).then(() => { this.me!.company.lock_auto = lock.checked; this.toast(t('saved')); void this.loadMe(); }).catch((e) => this.toast(e.message)));
      form.append(h('label', { class: 'inline' }, lock, t('companyLock')));
    }
    this.body.replaceChildren(form);
  }
}

export function unmount() {
  rootHost?.remove();
  rootHost = null;
}
