// Downloads a file produced by the server (exports, templates). The request
// carries the session cookie; errors come back as ApiError like any API call.
import { ApiError, qs, type Query } from '@/api/client';

function fileName(res: Response, fallback: string) {
  const cd = res.headers.get('Content-Disposition') ?? '';
  const star = cd.match(/filename\*=UTF-8''([^;]+)/i);
  if (star) {
    try {
      return decodeURIComponent(star[1]);
    } catch {
      /* fall through */
    }
  }
  const plain = cd.match(/filename="?([^";]+)"?/i);
  return plain ? plain[1] : fallback;
}

/** GETs path and saves (or, for PDFs with `view`, opens) the response. */
export async function downloadFile(path: string, query: Query, fallback: string, mode: 'save' | 'view' | 'print' = 'save') {
  // A window opened synchronously by the click is not blocked as a popup.
  const win = mode === 'view' ? window.open('', '_blank') : null;
  let res: Response;
  try {
    res = await fetch(`/api/v1${path}${qs(query)}`, { credentials: 'same-origin' });
  } catch {
    win?.close();
    throw new ApiError(0, { code: 'network', message: 'network error' });
  }
  if (!res.ok) {
    win?.close();
    const data = await res.json().catch(() => undefined);
    throw new ApiError(res.status, data?.error ?? { code: res.status >= 500 ? 'internal' : 'invalid_body', message: res.statusText });
  }
  const blob = await res.blob();
  const url = URL.createObjectURL(blob);
  if (mode === 'print') {
    const frame = document.createElement('iframe');
    frame.style.cssText = 'position:fixed;width:0;height:0;border:0;right:0;bottom:0';
    frame.src = url;
    frame.onload = () => setTimeout(() => { frame.contentWindow?.focus(); frame.contentWindow?.print(); }, 300);
    document.body.appendChild(frame);
    setTimeout(() => { frame.remove(); URL.revokeObjectURL(url); }, 120_000);
    return;
  }
  if (win && !win.closed) {
    win.location.href = url;
  } else {
    const a = document.createElement('a');
    a.href = url;
    a.download = fileName(res, fallback);
    document.body.appendChild(a);
    a.click();
    a.remove();
  }
  setTimeout(() => URL.revokeObjectURL(url), 120_000);
}
