import { useCallback, useEffect, useRef, useState, type RefObject } from 'react';

/** State persisted in localStorage (falls back to memory when storage is unavailable). */
export function usePersistentState<T>(key: string, initial: T) {
  const [value, setValue] = useState<T>(() => {
    try {
      const raw = localStorage.getItem(`tp.${key}`);
      return raw ? { ...initial, ...JSON.parse(raw) } : initial;
    } catch {
      return initial;
    }
  });
  useEffect(() => {
    try {
      localStorage.setItem(`tp.${key}`, JSON.stringify(value));
    } catch {
      /* ignore */
    }
  }, [key, value]);
  return [value, setValue] as const;
}

export function useDebounced<T>(value: T, ms = 250) {
  const [v, setV] = useState(value);
  useEffect(() => {
    const id = setTimeout(() => setV(value), ms);
    return () => clearTimeout(id);
  }, [value, ms]);
  return v;
}

/** Most-recently-used ids per kind (accounts, parties…) for quick picking. */
export function useRecent(kind: string, max = 6) {
  const key = `tp.recent.${kind}`;
  const read = useCallback((): number[] => {
    try {
      return JSON.parse(localStorage.getItem(key) ?? '[]');
    } catch {
      return [];
    }
  }, [key]);
  const push = useCallback(
    (id: number) => {
      try {
        const next = [id, ...read().filter((x) => x !== id)].slice(0, max);
        localStorage.setItem(key, JSON.stringify(next));
      } catch {
        /* ignore */
      }
    },
    [key, max, read],
  );
  return { read, push };
}

export function useClickOutside<T extends HTMLElement>(onOutside: () => void, active: boolean, ...also: RefObject<HTMLElement | null>[]) {
  const ref = useRef<T>(null);
  useEffect(() => {
    if (!active) return;
    const handler = (e: MouseEvent) => {
      const t = e.target as Node;
      if (ref.current && !ref.current.contains(t) && !also.some((r) => r.current?.contains(t))) onOutside();
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, [onOutside, active]);
  return ref;
}

/** Simple form state with server-side field errors. */
export function useFormState<T extends object>(initial: T) {
  const [values, setValues] = useState<T>(initial);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const set = useCallback(<K extends keyof T>(k: K, v: T[K]) => {
    setValues((s) => ({ ...s, [k]: v }));
    setErrors((e) => {
      if (!e[k as string]) return e;
      const n = { ...e };
      delete n[k as string];
      return n;
    });
  }, []);
  return { values, setValues, set, errors, setErrors };
}
