import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react';
import { en, type Dictionary } from './en';
import { fa } from './fa';

export type Lang = 'fa' | 'en';

const dictionaries: Record<Lang, Dictionary> = { fa, en };

type Join<K, P> = K extends string ? (P extends string ? `${K}.${P}` : never) : never;
type Paths<T> = { [K in keyof T & string]: T[K] extends string ? K : Join<K, Paths<T[K]>> }[keyof T & string];
export type TKey = Paths<Dictionary>;
export type Params = Record<string, string | number | null | undefined>;

interface I18nContext {
  lang: Lang;
  dir: 'rtl' | 'ltr';
  setLang: (l: Lang) => void;
  t: (key: TKey, params?: Params) => string;
  /** Looks up a dynamic key (e.g. from an API code); returns undefined if missing. */
  tryT: (key: string, params?: Params) => string | undefined;
}

const Ctx = createContext<I18nContext | null>(null);

function lookup(dict: Dictionary, key: string): string | undefined {
  let cur: unknown = dict;
  for (const part of key.split('.')) {
    if (cur && typeof cur === 'object' && part in (cur as Record<string, unknown>)) cur = (cur as Record<string, unknown>)[part];
    else return undefined;
  }
  return typeof cur === 'string' ? cur : undefined;
}

// Tafakor AI tag: the most recent i18n key that produced each text, so shared
// components can derive language-independent data-ai-* tags from their labels.
const keyByText = new Map<string, string>();
/** Returns the i18n key a visible text came from (undefined for data). */
export function aiKeyOf(text: string): string | undefined {
  return keyByText.get(text);
}

function interpolate(s: string, params?: Params) {
  if (!params) return s;
  return s.replace(/\{(\w+)\}/g, (_, k) => (params[k] === undefined || params[k] === null ? '' : String(params[k])));
}

function initialLang(): Lang {
  try {
    const v = localStorage.getItem('tp.lang');
    if (v === 'fa' || v === 'en') return v;
  } catch {
    /* storage unavailable */
  }
  return 'fa';
}

export function I18nProvider({ children }: { children: ReactNode }) {
  const [lang, setLangState] = useState<Lang>(initialLang);
  const dir = lang === 'fa' ? 'rtl' : 'ltr';

  useEffect(() => {
    document.documentElement.lang = lang;
    document.documentElement.dir = dir;
    document.title = dictionaries[lang].app.name;
  }, [lang, dir]);

  const setLang = useCallback((l: Lang) => {
    setLangState(l);
    try {
      localStorage.setItem('tp.lang', l);
    } catch {
      /* ignore */
    }
  }, []);

  const value = useMemo<I18nContext>(() => {
    const dict = dictionaries[lang];
    const tryT = (key: string, params?: Params) => {
      const s = lookup(dict, key) ?? lookup(en, key);
      if (s === undefined) return undefined;
      const out = interpolate(s, params);
      keyByText.set(out, key);
      return out;
    };
    return { lang, dir, setLang, tryT, t: (key, params) => tryT(key, params) ?? key };
  }, [lang, dir, setLang]);

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useI18n() {
  const c = useContext(Ctx);
  if (!c) throw new Error('useI18n outside I18nProvider');
  return c;
}
