// Printable documents of the application. Each builder turns domain data into
// a PdfDocument; the server renders all of them inside the same letterhead.
import { api } from '@/api/client';
import type { Company, Expense, Invoice, JournalEntry, Party, StockDocument, TreasuryTx } from '@/api/types';
import type { TKey, Params } from '@/i18n/I18nProvider';
import type { Formatter } from '@/i18n/format';
import type { PdfDocument, PdfKV } from './pdf';

type T = (key: TKey, params?: Params) => string;
type TryT = (key: string, params?: Params) => string | undefined;

const kv = (label: string, value: string | null | undefined): PdfKV[] => (value ? [{ label, value }] : []);

async function maybe<R>(p: Promise<R>): Promise<R | null> {
  try {
    return await p;
  } catch {
    return null;
  }
}

function partyLines(p: { national_id?: string; economic_code?: string; phone?: string; mobile?: string; address?: string } | null, t: T, f: Formatter): PdfKV[] {
  if (!p) return [];
  return [
    ...kv(t('parties.nationalId'), p.national_id ? f.digits(p.national_id) : ''),
    ...kv(t('parties.economicCode'), p.economic_code ? f.digits(p.economic_code) : ''),
    ...kv(t('parties.phone'), p.phone || p.mobile ? f.digits(p.phone || p.mobile || '') : ''),
    ...kv(t('parties.address'), p.address),
  ];
}

export async function invoiceDocument(inv: Invoice, t: T, f: Formatter, lang: string): Promise<PdfDocument> {
  const [company, party] = await Promise.all([maybe(api.get<Company>('/company')), maybe(api.get<Party>(`/parties/${inv.party_id}`))]);
  const key = `${inv.side}_${inv.kind}` as 'sales_invoice';
  const companyName = company ? (lang === 'en' && company.name_en ? company.name_en : company.name) : '';
  const us = { title: '', name: companyName, lines: partyLines(company, t, f) };
  const them = { title: '', name: inv.party_name, lines: partyLines(party, t, f) };
  const [seller, buyer] = inv.side === 'sales' ? [us, them] : [them, us];
  seller.title = t('invoices.seller');
  buyer.title = t('invoices.buyer');
  const posted = inv.status === 'posted' && inv.kind !== 'quotation';
  return {
    title: t(`invoices.singular.${key}`),
    number: f.int(inv.number),
    date: f.date(inv.date),
    meta: [
      ...kv(t('invoices.dueDate'), f.date(inv.due_date)),
      ...kv(t('common.reference'), inv.reference),
      ...kv(t('common.status'), t(`status.${inv.status}`)),
    ],
    parties: [seller, buyer],
    tables: [{
      columns: [
        { label: '#', align: 'center' },
        { label: t('invoices.item'), align: 'start' },
        { label: t('invoices.quantity'), align: 'end' },
        { label: t('invoices.unit'), align: 'start' },
        { label: t('invoices.unitPrice'), align: 'end' },
        { label: t('invoices.discount'), align: 'end' },
        { label: t('invoices.tax'), align: 'end' },
        { label: t('invoices.lineTotal'), align: 'end' },
      ],
      rows: (inv.items ?? []).map((it, i) => ({
        cells: [
          f.int(i + 1),
          [it.product_name, it.description].filter(Boolean).join(' — '),
          f.qty(it.quantity),
          it.unit,
          f.money(it.unit_price),
          f.money(it.discount),
          f.money(it.tax_amount ?? 0),
          f.money(it.total ?? 0),
        ],
      })),
    }],
    totals: [
      { label: t('invoices.subtotal'), value: f.money(inv.subtotal) },
      { label: t('invoices.discountTotal'), value: f.money(inv.discount_total) },
      { label: t('invoices.taxTotal'), value: f.money(inv.tax_total) },
      { label: `${t('invoices.grandTotal')}${company ? ` (${company.base_currency})` : ''}`, value: f.money(inv.total), strong: true },
      ...(posted ? [{ label: t('invoices.paid'), value: f.money(inv.paid_amount) }, { label: t('invoices.outstanding'), value: f.money(inv.outstanding), strong: true }] : []),
    ],
    notes: inv.notes,
    signatures: [t('invoices.seller'), t('invoices.buyer')],
  };
}

export function journalDocument(e: JournalEntry, t: T, tryT: TryT, f: Formatter, accountName: (a: { name: string; name_en?: string }) => string): PdfDocument {
  return {
    title: t('journal.entry', { number: f.int(e.number) }),
    number: f.int(e.number),
    date: f.date(e.date),
    meta: [
      ...kv(t('journal.source'), tryT(`journal.sources.${e.source_type}`) ?? e.source_type),
      ...kv(t('common.status'), t(`status.${e.status}`)),
      ...kv(t('common.reference'), e.reference),
    ],
    paragraphs: e.description ? [e.description] : [],
    tables: [{
      columns: [
        { label: t('common.code'), align: 'start' },
        { label: t('journal.account'), align: 'start' },
        { label: t('journal.party'), align: 'start' },
        { label: t('common.description'), align: 'start' },
        { label: t('common.debit'), align: 'end' },
        { label: t('common.credit'), align: 'end' },
      ],
      rows: (e.lines ?? []).map((l) => ({
        cells: [f.digits(l.account_code ?? ''), accountName({ name: l.account_name ?? '', name_en: l.account_name_en }), l.party_name ?? '', l.description,
          Number(l.debit) ? f.money(l.debit) : '', Number(l.credit) ? f.money(l.credit) : ''],
      })),
      footer: ['', '', '', t('common.total'), f.money(e.total_debit), f.money(e.total_credit)],
    }],
    signatures: [t('journal.preparedBy'), t('journal.approvedBy')],
  };
}

export function treasuryDocument(x: TreasuryTx, t: T, tryT: TryT, f: Formatter): PdfDocument {
  const counterpart = x.party_name ?? x.counter_account_name ?? x.to_treasury_account_name ?? '';
  return {
    title: t(`treasury.vouchers.${x.kind}`),
    number: f.int(x.number),
    date: f.date(x.date),
    meta: [
      ...kv(x.kind === 'transfer' ? t('treasury.fromAccount') : t('treasury.treasuryAccount'), x.treasury_account_name),
      ...kv(x.kind === 'transfer' ? t('treasury.toAccount') : t('treasury.counterpart'), counterpart),
      ...kv(t('common.reference'), x.reference),
      ...kv(t('common.status'), t(`status.${x.status}`)),
    ],
    paragraphs: x.description ? [x.description] : [],
    tables: x.allocations?.length ? [{
      title: t('treasury.allocations'),
      columns: [{ label: t('inventory.document'), align: 'start' }, { label: t('common.amount'), align: 'end' }],
      rows: x.allocations.map((a) => ({ cells: [`${tryT(`invoices.singular.${a.side}_${a.kind}`) ?? ''} ${f.int(a.number)}`, f.money(a.amount)] })),
    }] : [],
    totals: [{ label: t('common.amount'), value: f.money(x.amount), strong: true }],
    signatures: x.kind === 'receipt' ? [t('treasury.payer'), t('treasury.receiver')] : [t('treasury.receiver'), t('treasury.payer')],
  };
}

export function expenseDocument(x: Expense, t: T, f: Formatter): PdfDocument {
  return {
    title: t('expenses.voucher'),
    number: f.int(x.number),
    date: f.date(x.date),
    meta: [
      ...kv(t('expenses.category'), x.category_name),
      ...kv(t('expenses.account'), x.account_name),
      ...kv(t('expenses.paymentMethod'), `${t(`expenses.methods.${x.payment_method}`)} — ${x.treasury_account_name ?? x.party_name ?? ''}`),
      ...kv(t('journal.costCenter'), x.cost_center_name),
      ...kv(t('common.reference'), x.reference),
      ...kv(t('common.status'), t(`status.${x.status}`)),
    ],
    paragraphs: x.description ? [x.description] : [],
    totals: [{ label: t('common.amount'), value: f.money(x.amount), strong: true }],
    signatures: [t('journal.preparedBy'), t('journal.approvedBy')],
  };
}

export function stockDocument(d: StockDocument, t: T, f: Formatter): PdfDocument {
  const showCost = d.kind === 'receipt' || d.kind === 'adjustment';
  return {
    title: t(`inventory.kinds.${d.kind}`),
    number: f.int(d.number),
    date: f.date(d.date),
    meta: [
      ...kv(t('inventory.warehouse'), d.warehouse_name),
      ...kv(t('inventory.toWarehouse'), d.to_warehouse_name),
      ...kv(t('inventory.counterAccount'), d.counter_account_name),
      ...kv(t('common.reference'), d.reference),
      ...kv(t('common.status'), t(`status.${d.status}`)),
    ],
    paragraphs: d.description ? [d.description] : [],
    tables: [{
      columns: [
        { label: '#', align: 'center' },
        { label: t('products.sku'), align: 'start' },
        { label: t('invoices.item'), align: 'start' },
        { label: t('inventory.quantity'), align: 'end' },
        ...(showCost ? [{ label: t('inventory.unitCost'), align: 'end' as const }] : []),
        { label: t('common.description'), align: 'start' },
      ],
      rows: (d.lines ?? []).map((l, i) => ({
        cells: [f.int(i + 1), f.digits(l.product_sku ?? ''), l.product_name ?? '', `${f.qty(l.quantity)} ${l.unit ?? ''}`,
          ...(showCost ? [Number(l.unit_cost) ? f.money(l.unit_cost) : ''] : []), l.description],
      })),
    }],
    signatures: [t('inventory.deliveredBy'), t('inventory.receivedBy')],
  };
}
