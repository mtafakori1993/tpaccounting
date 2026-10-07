import type { InvoiceKind, Side } from '@/api/types';

export interface DocInfo {
  routeSide: 'sales' | 'purchases';
  routeKind: 'invoices' | 'quotations' | 'returns';
  side: Side;
  kind: InvoiceKind;
  /** e.g. sales_invoice — used for translation keys */
  key: 'sales_invoice' | 'sales_quotation' | 'sales_return' | 'purchase_invoice' | 'purchase_return';
  module: 'sales' | 'purchases';
  api: string;
  base: string;
  partyRole: 'customer' | 'supplier';
}

const kinds: Record<string, InvoiceKind> = { invoices: 'invoice', quotations: 'quotation', returns: 'return' };

export function docInfo(routeSide?: string, routeKind?: string): DocInfo | null {
  if ((routeSide !== 'sales' && routeSide !== 'purchases') || !routeKind || !(routeKind in kinds)) return null;
  if (routeSide === 'purchases' && routeKind === 'quotations') return null;
  const side: Side = routeSide === 'sales' ? 'sales' : 'purchase';
  const kind = kinds[routeKind];
  return {
    routeSide,
    routeKind: routeKind as DocInfo['routeKind'],
    side,
    kind,
    key: `${side}_${kind}` as DocInfo['key'],
    module: routeSide,
    api: `/${routeSide}/${routeKind}`,
    base: `/${routeSide}/${routeKind}`,
    partyRole: side === 'sales' ? 'customer' : 'supplier',
  };
}

export function routeFor(side: Side, kind: InvoiceKind, id?: number) {
  const s = side === 'sales' ? 'sales' : 'purchases';
  const k = kind === 'invoice' ? 'invoices' : kind === 'return' ? 'returns' : 'quotations';
  return `/${s}/${k}${id ? `/${id}` : ''}`;
}
