/** Route of the business document that generated a journal entry. */
export function sourceRoute(sourceType: string, sourceId: number | null): string | null {
  if (!sourceId) return null;
  switch (sourceType) {
    case 'sales_invoice': return `/sales/invoices/${sourceId}`;
    case 'sales_return': return `/sales/returns/${sourceId}`;
    case 'purchase_invoice': return `/purchases/invoices/${sourceId}`;
    case 'purchase_return': return `/purchases/returns/${sourceId}`;
    case 'treasury_receipt':
    case 'treasury_payment':
    case 'treasury_transfer': return `/treasury/transactions?view=${sourceId}`;
    case 'expense': return `/expenses?view=${sourceId}`;
    case 'stock_document': return `/inventory/documents/${sourceId}`;
    case 'check': return `/checks/${sourceId}`;
    case 'payroll':
    case 'payroll_payment': return `/payroll/periods/${sourceId}`;
    case 'reversal': return `/journal/${sourceId}`;
    case 'opening': return '/treasury';
    case 'closing': return '/settings/fiscal-years';
    default: return null;
  }
}
