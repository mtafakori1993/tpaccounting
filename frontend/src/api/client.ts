// HTTP client for the REST API. Sends the CSRF double-submit header and turns
// every failure into an ApiError with a stable code for translation.

export interface ApiErrorBody {
  code: string;
  message: string;
  fields?: Record<string, string>;
  params?: Record<string, string | number | boolean>;
}

export class ApiError extends Error {
  status: number;
  code: string;
  fields: Record<string, string>;
  params: Record<string, string | number | boolean>;
  constructor(status: number, body: ApiErrorBody) {
    super(body.message);
    this.status = status;
    this.code = body.code;
    this.fields = body.fields ?? {};
    this.params = body.params ?? {};
  }
}

const BASE = '/api/v1';

function csrfToken() {
  const m = document.cookie.match(/(?:^|;\s*)tp_csrf=([^;]+)/);
  return m ? decodeURIComponent(m[1]) : '';
}

let onUnauthorized: (() => void) | null = null;
export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn;
}

export type Query = Record<string, string | number | boolean | null | undefined>;

/** Answer to a budget-control prompt: confirm a soft limit, or override a hard limit with a reason. */
export type BudgetDecision = { confirm: true } | { override: string } | null;
let budgetPrompt: ((err: ApiError) => Promise<BudgetDecision>) | null = null;
let budgetWarnings: ((warnings: unknown[]) => void) | null = null;
/** Registers the UI that asks the user how to proceed when a posting exceeds a budget. */
export function setBudgetHandlers(prompt: typeof budgetPrompt, warnings: typeof budgetWarnings) {
  budgetPrompt = prompt;
  budgetWarnings = warnings;
}

export function qs(q?: Query) {
  if (!q) return '';
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(q)) {
    if (v !== undefined && v !== null && v !== '') p.set(k, String(v));
  }
  const s = p.toString();
  return s ? `?${s}` : '';
}

export async function request<T>(method: string, path: string, body?: unknown, query?: Query, extra?: Record<string, string>): Promise<T> {
  try {
    return await send<T>(method, path, body, query, extra);
  } catch (e) {
    // Budget control: soft limits need confirmation, hard limits an authorised override with a reason.
    if (e instanceof ApiError && budgetPrompt && !extra && (e.code === 'budget_confirmation_required' || (e.code === 'budget_exceeded' && e.params.can_override === true))) {
      const d = await budgetPrompt(e);
      if (d && 'confirm' in d) return send<T>(method, path, body, query, { 'X-Budget-Confirm': 'true' });
      if (d && 'override' in d) return send<T>(method, path, body, query, { 'X-Budget-Override': encodeURIComponent(d.override) });
    }
    throw e;
  }
}

async function send<T>(method: string, path: string, body?: unknown, query?: Query, extra?: Record<string, string>): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json', ...extra };
  let payload: BodyInit | undefined;
  if (body instanceof FormData) {
    payload = body;
  } else if (body !== undefined) {
    headers['Content-Type'] = 'application/json';
    payload = JSON.stringify(body);
  }
  if (method !== 'GET') headers['X-CSRF-Token'] = csrfToken();
  let res: Response;
  try {
    res = await fetch(BASE + path + qs(query), { method, headers, body: payload, credentials: 'same-origin' });
  } catch {
    throw new ApiError(0, { code: 'network', message: 'network error' });
  }
  const warn = res.headers.get('X-Budget-Warnings');
  if (warn && budgetWarnings) {
    try {
      budgetWarnings(JSON.parse(decodeURIComponent(warn.replace(/\+/g, ' '))));
    } catch {
      /* malformed header: ignore */
    }
  }
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  let data: unknown = undefined;
  try {
    data = text ? JSON.parse(text) : undefined;
  } catch {
    data = undefined;
  }
  if (!res.ok) {
    const err = (data as { error?: ApiErrorBody } | undefined)?.error ?? { code: res.status >= 500 ? 'internal' : 'invalid_body', message: res.statusText };
    if (res.status === 401 && path !== '/auth/login' && onUnauthorized) onUnauthorized();
    throw new ApiError(res.status, err);
  }
  return data as T;
}

export const api = {
  get: <T,>(path: string, query?: Query) => request<T>('GET', path, undefined, query),
  post: <T,>(path: string, body?: unknown) => request<T>('POST', path, body ?? {}),
  put: <T,>(path: string, body?: unknown) => request<T>('PUT', path, body ?? {}),
  del: <T = void,>(path: string) => request<T>('DELETE', path),
};
