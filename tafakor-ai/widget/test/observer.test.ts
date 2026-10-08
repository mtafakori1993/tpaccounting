// @vitest-environment happy-dom
import { describe, expect, it } from 'vitest';
import { waitFor } from '../src/agent/observer';

describe('observer.waitFor', () => {
  it('resolves when the DOM changes', async () => {
    setTimeout(() => document.body.append(Object.assign(document.createElement('div'), { id: 'late' })), 20);
    const el = await waitFor(() => document.getElementById('late'), 1000);
    expect(el?.id).toBe('late');
  });
  it('gives up after the timeout (stop safely)', async () => {
    expect(await waitFor(() => document.getElementById('never'), 50)).toBeNull();
  });
});
