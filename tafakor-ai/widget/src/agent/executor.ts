// Executor: runs a server-issued UI plan step by step through the human-interaction
// engine (engine.ts). It adds no interaction logic of its own.
//
// Two kinds of plan:
//   HUMAN_UI (default) — really operates the application: clicks, types, submits, observes.
//   preview            — UI_PREVIEW: fills the real form but never submits; the user saves it.
// Every important step is verified by the engine; on any mismatch it stops and reports.
// It never falls back to the API: a failure is reported, and the user decides.

import { Cursor } from './cursor';
import { HumanUIEngine, executeHumanUIAction, type UIStep } from './engine';

export type { UIStep } from './engine';

export interface UIPlan {
  task_id: string;
  steps: UIStep[];
  // 'preview': fill the real form but never submit — the user reviews and saves it by hand.
  mode?: 'preview';
  // Always 'human_ui' for plans the assistant operates itself.
  exec_mode?: string;
  step?: string;
  // preview only: the save steps left to the user (watched, never run)
  watch?: UIStep[];
}

export interface UIResult {
  ok: boolean;
  failed_step: number;
  error: string;
  // The preview plan was filled in; nothing was submitted.
  preview?: boolean;
  // After a preview: the user saved the form themselves (the server verifies it).
  user_saved?: boolean;
}

/** The steps a preview leaves to the user (save and what follows it). */
export function previewRemainder(plan: UIPlan): UIStep[] {
  if (plan.watch?.length) return plan.watch;
  const i = plan.steps.findIndex((s) => s.op === 'submit' || s.op === 'wait_closed' || s.op === 'wait_leave');
  return i < 0 ? [] : plan.steps.slice(i);
}

/** After a preview, waits for the user to save (or close) the open form themselves. */
export async function watchUserSave(plan: UIPlan, cursor: Cursor, maxMs?: number) {
  return new HumanUIEngine(cursor).watchUserSave(previewRemainder(plan), maxMs);
}

export async function runPlan(plan: UIPlan, cursor: Cursor, onStatus: (label: string) => void, aborted: () => boolean): Promise<UIResult> {
  const preview = plan.mode === 'preview';
  const engine = new HumanUIEngine(cursor);
  cursor.show();
  try {
    for (let i = 0; i < plan.steps.length; i++) {
      if (aborted()) return { ok: false, failed_step: i, error: 'cancelled' };
      const s = plan.steps[i];
      // Preview: fill the form and stop — the user reviews and submits it themselves.
      if (preview && (s.op === 'submit' || s.op === 'wait_closed' || s.op === 'wait_leave')) break;
      onStatus(s.label);
      cursor.label.textContent = s.label;
      const err = await executeHumanUIAction(engine, s);
      if (err) return { ok: false, failed_step: i, error: err };
    }
    return { ok: true, failed_step: -1, error: '', preview };
  } catch (e: any) {
    return { ok: false, failed_step: -1, error: String(e?.message ?? e) };
  } finally {
    setTimeout(() => cursor.hide(), 800);
  }
}
