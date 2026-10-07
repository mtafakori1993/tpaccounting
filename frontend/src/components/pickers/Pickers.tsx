import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from '@/api/client';
import { useAccounts, useCostCenters, useTreasuryAccounts, useWarehouses } from '@/api/queries';
import type { Account, Page, Party, Product } from '@/api/types';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { useRecent } from '@/lib/hooks';
import { normalizeDigits } from '@/lib/dates';
import { ComboBox, type ComboOption } from '../ui/ComboBox';
import { Select } from '../ui/Field';

type Aria = { id?: string; 'aria-invalid'?: boolean; 'aria-describedby'?: string };

export function useAccountName() {
  const { lang } = useI18n();
  return (a: { name: string; name_en?: string }) => (lang === 'en' && a.name_en ? a.name_en : a.name);
}

/** Account picker over the cached chart. By default only postable, active accounts. */
export function AccountPicker({ value, onChange, filter, includeGroups, autoFocus, ...aria }: {
  value: number | null;
  onChange: (id: number | null, account?: Account) => void;
  filter?: (a: Account) => boolean;
  includeGroups?: boolean;
  autoFocus?: boolean;
} & Aria) {
  const { data } = useAccounts();
  const name = useAccountName();
  const recent = useRecent('accounts');
  const usable = useMemo(
    () => (data ?? []).filter((a) => a.is_active && (includeGroups || a.postable) && (!filter || filter(a))),
    [data, includeGroups, filter],
  );
  const toOpt = (a: Account): ComboOption => ({ id: a.id, code: a.code, label: name(a) });
  const current = data?.find((a) => a.id === value);
  const recentOpts = recent.read().map((id) => usable.find((a) => a.id === id)).filter(Boolean).map((a) => toOpt(a!));
  return (
    <ComboBox
      value={value}
      valueLabel={current ? `${current.code} — ${name(current)}` : ''}
      recent={recentOpts}
      autoFocus={autoFocus}
      onChange={(id) => {
        if (id) recent.push(id);
        onChange(id, data?.find((a) => a.id === id));
      }}
      search={(q) => {
        const s = normalizeDigits(q).toLowerCase();
        return usable
          .filter((a) => !s || a.code.startsWith(s) || a.name.toLowerCase().includes(s) || a.name_en.toLowerCase().includes(s))
          .slice(0, 60)
          .map(toOpt);
      }}
      {...aria}
    />
  );
}

/** Party picker with server-side search; role limits to customers or suppliers. */
export function PartyPicker({ value, onChange, role, autoFocus, ...aria }: {
  value: number | null;
  onChange: (id: number | null, party?: Party) => void;
  role?: 'customer' | 'supplier';
  autoFocus?: boolean;
} & Aria) {
  const f = useFormat();
  const recent = useRecent('parties');
  const { data: current } = useQuery({ queryKey: ['party', value], queryFn: () => api.get<Party>(`/parties/${value}`), enabled: !!value, staleTime: 30_000 });
  const { data: recentList } = useQuery({
    queryKey: ['parties', 'recent', role, recent.read().join(',')],
    queryFn: async () => {
      const ids = recent.read();
      if (!ids.length) return [];
      const found = await Promise.all(ids.map((id) => api.get<Party>(`/parties/${id}`).catch(() => null)));
      return found.filter((p): p is Party => !!p && p.is_active && (!role || (role === 'customer' ? p.is_customer : p.is_supplier)));
    },
    staleTime: 30_000,
  });
  const cache = useMemo(() => new Map<number, Party>(), []);
  const toOpt = (p: Party): ComboOption => ({ id: p.id, code: p.code, label: p.name, meta: f.balance(p.balance) });
  return (
    <ComboBox
      value={value}
      valueLabel={current?.name ?? ''}
      recent={(recentList ?? []).map(toOpt)}
      autoFocus={autoFocus}
      onChange={(id) => {
        if (id) recent.push(id);
        onChange(id, id ? cache.get(id) : undefined);
      }}
      search={async (q) => {
        const page = await api.get<Page<Party>>('/parties', { q: normalizeDigits(q), role, active: true, page_size: 30 });
        page.items.forEach((p) => cache.set(p.id, p));
        recentList?.forEach((p) => cache.set(p.id, p));
        return page.items.map(toOpt);
      }}
      {...aria}
    />
  );
}

export function ProductPicker({ value, valueLabel, onChange, autoFocus, ...aria }: {
  value: number | null;
  valueLabel?: string;
  onChange: (id: number | null, product?: Product) => void;
  autoFocus?: boolean;
} & Aria) {
  const f = useFormat();
  const { lang } = useI18n();
  const recent = useRecent('products');
  const cache = useMemo(() => new Map<number, Product>(), []);
  const { data: current } = useQuery({
    queryKey: ['product', value],
    queryFn: () => api.get<Product>(`/products/${value}`),
    enabled: !!value && !valueLabel,
    staleTime: 30_000,
  });
  const label = (p: Product) => (lang === 'en' && p.name_en ? p.name_en : p.name);
  return (
    <ComboBox
      value={value}
      valueLabel={valueLabel ?? (current ? label(current) : '')}
      autoFocus={autoFocus}
      onChange={(id) => {
        if (id) recent.push(id);
        onChange(id, id ? cache.get(id) : undefined);
      }}
      search={async (q) => {
        const page = await api.get<Page<Product>>('/products', { q: normalizeDigits(q), active: true, page_size: 30 });
        page.items.forEach((p) => cache.set(p.id, p));
        const ids = recent.read();
        const items = q ? page.items : [...page.items].sort((a, b) => (ids.indexOf(b.id) + 1 || 0) - (ids.indexOf(a.id) + 1 || 0));
        return items.map((p) => ({ id: p.id, code: p.sku, label: label(p), meta: p.track_inventory ? f.qty(p.on_hand) : undefined }));
      }}
      {...aria}
    />
  );
}

/** Select for small lists: cost centers. */
export function CostCenterSelect({ value, onChange, kinds, emptyLabel, ...aria }: { value: number | null; onChange: (id: number | null) => void; kinds?: string[]; emptyLabel?: string } & Aria) {
  const { data } = useCostCenters();
  const { t, lang } = useI18n();
  return (
    <Select value={value ?? ''} onChange={(e) => onChange(e.target.value ? Number(e.target.value) : null)} {...aria}>
      <option value="">{emptyLabel ?? t('common.none')}</option>
      {data?.filter((c) => (c.is_active || c.id === value) && (!kinds || kinds.includes(c.kind))).map((c) => (
        <option key={c.id} value={c.id}>{lang === 'en' && c.name_en ? c.name_en : c.name}</option>
      ))}
    </Select>
  );
}

export function WarehouseSelect({ value, onChange, allowEmpty, ...aria }: { value: number | null; onChange: (id: number | null) => void; allowEmpty?: boolean } & Aria) {
  const { data } = useWarehouses();
  const { t } = useI18n();
  return (
    <Select value={value ?? ''} onChange={(e) => onChange(e.target.value ? Number(e.target.value) : null)} {...aria}>
      {(allowEmpty || !value) && <option value="">{allowEmpty ? t('common.all') : t('common.select')}</option>}
      {data?.filter((w) => w.is_active || w.id === value).map((w) => <option key={w.id} value={w.id}>{w.name}</option>)}
    </Select>
  );
}

export function TreasurySelect({ value, onChange, kind, allowEmpty, ...aria }: { value: number | null; onChange: (id: number | null) => void; kind?: 'cash' | 'bank'; allowEmpty?: boolean } & Aria) {
  const { data } = useTreasuryAccounts();
  const { t } = useI18n();
  const f = useFormat();
  return (
    <Select value={value ?? ''} onChange={(e) => onChange(e.target.value ? Number(e.target.value) : null)} {...aria}>
      <option value="">{allowEmpty ? t('common.all') : t('common.select')}</option>
      {data?.filter((a) => (!kind || a.kind === kind) && (a.is_active || a.id === value)).map((a) => (
        <option key={a.id} value={a.id}>{a.name} ({f.money(a.balance)})</option>
      ))}
    </Select>
  );
}
