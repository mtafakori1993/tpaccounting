// Same-origin client for /ai-api. Sends the app's CSRF double-submit token; the
// session cookie travels automatically. Server-sent events are read from a POST
// response stream.

const BASE = '/ai-api';

export function csrf(): string {
  const m = document.cookie.match(/(?:^|;\s*)tp_csrf=([^;]+)/);
  return m ? decodeURIComponent(m[1]) : '';
}

export class ApiError extends Error {
  constructor(public status: number, public code: string, message: string) {
    super(message);
  }
}

async function parse<T>(res: Response): Promise<T> {
  const text = await res.text();
  let data: any;
  try {
    data = text ? JSON.parse(text) : undefined;
  } catch {
    data = undefined;
  }
  if (!res.ok) {
    const e = data?.error ?? {};
    throw new ApiError(res.status, e.code ?? 'http_' + res.status, e.message ?? res.statusText);
  }
  return data as T;
}

export async function get<T>(path: string): Promise<T> {
  const res = await fetch(BASE + path, { credentials: 'same-origin', headers: { Accept: 'application/json' } });
  return parse<T>(res);
}

export async function send<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(BASE + path, {
    method,
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json', Accept: 'application/json', 'X-CSRF-Token': csrf() },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  return parse<T>(res);
}

export async function upload(file: Blob, name: string): Promise<{ id: string; name: string; content_type: string; size: number }> {
  const fd = new FormData();
  fd.append('file', file, name);
  const res = await fetch(BASE + '/uploads', { method: 'POST', credentials: 'same-origin', headers: { 'X-CSRF-Token': csrf() }, body: fd });
  return parse(res);
}

export async function transcribe(audio: Blob, lang: string): Promise<{ text: string; normalized: string }> {
  const res = await fetch(`${BASE}/speech?lang=${lang}`, {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'X-CSRF-Token': csrf(), 'Content-Type': audio.type || 'audio/webm' },
    body: audio,
  });
  return parse(res);
}

export type Handler = (event: string, data: any) => void;

/** POSTs a JSON body and dispatches each server-sent event as it arrives. */
export async function stream(path: string, body: unknown, on: Handler, signal?: AbortSignal): Promise<void> {
  const res = await fetch(BASE + path, {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json', Accept: 'text/event-stream', 'X-CSRF-Token': csrf() },
    body: JSON.stringify(body ?? {}),
    signal,
  });
  if (!res.ok || !res.body) {
    await parse(res);
    return;
  }
  const reader = res.body.getReader();
  const dec = new TextDecoder();
  let buf = '';
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    buf += dec.decode(value, { stream: true });
    let i: number;
    while ((i = buf.indexOf('\n\n')) >= 0) {
      const block = buf.slice(0, i);
      buf = buf.slice(i + 2);
      let ev = 'message';
      const data: string[] = [];
      for (const line of block.split('\n')) {
        if (line.startsWith('event:')) ev = line.slice(6).trim();
        else if (line.startsWith('data:')) data.push(line.slice(5).trim());
      }
      if (!data.length) continue;
      try {
        on(ev, JSON.parse(data.join('\n')));
      } catch {
        /* ignore malformed event */
      }
    }
  }
}
