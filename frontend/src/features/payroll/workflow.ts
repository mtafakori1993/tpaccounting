import type { PayrollPeriod } from '@/api/types';
import type { Step } from '@/components/ui/Steps';

export const workflowSteps = ['inputs', 'calculate', 'review', 'warnings', 'approve', 'post', 'pay', 'close'] as const;
export type WorkflowStep = (typeof workflowSteps)[number];

/** The step the user should do next, from the payroll status (null when closed). */
export function currentStep(p: Pick<PayrollPeriod, 'status' | 'warnings'>): WorkflowStep | null {
  switch (p.status) {
    case 'draft':
      return 'inputs';
    case 'calculated':
      return (p.warnings?.length ?? 0) > 0 ? 'warnings' : 'approve';
    case 'approved':
      return 'post';
    case 'posted':
      return 'pay';
    case 'paid':
      return 'close';
    default:
      return null;
  }
}

/** The workflow action that completes a step (what the primary button does). */
export const stepAction: Partial<Record<WorkflowStep, string>> = {
  inputs: 'calculate', calculate: 'calculate', review: 'approve', warnings: 'approve', approve: 'approve', post: 'post', pay: 'pay', close: 'close',
};

export const actionPermission: Record<string, string> = {
  calculate: 'payroll.calculate', approve: 'payroll.approve', reopen: 'payroll.approve', post: 'payroll.post', unpost: 'payroll.post',
  pay: 'payroll.pay', unpay: 'payroll.pay', close: 'payroll.close',
};

/** Stepper states for a payroll. */
export function stepStates(p: Pick<PayrollPeriod, 'status' | 'warnings'>, label: (s: WorkflowStep) => string, meta: Partial<Record<WorkflowStep, string>> = {}): Step[] {
  const cur = currentStep(p);
  const idx = cur ? workflowSteps.indexOf(cur) : workflowSteps.length;
  return workflowSteps.map((s, i) => ({
    key: s,
    label: label(s),
    meta: meta[s],
    state: i < idx ? 'done' : i === idx ? 'current' : 'upcoming',
  }));
}
