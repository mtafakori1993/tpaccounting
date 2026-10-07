import { api } from '@/api/client';
import type { Party } from '@/api/types';
import { useI18n } from '@/i18n/I18nProvider';
import { Button } from '@/components/ui/Button';
import { Checkbox, Field, Input, Select, TextArea } from '@/components/ui/Field';
import { Alert } from '@/components/ui/Misc';
import { Dialog } from '@/components/ui/Dialog';
import { AccountPicker } from '@/components/pickers/Pickers';
import { useFormState } from '@/lib/hooks';
import { useSubmit } from '@/lib/useSubmit';
import { normalizeDigits } from '@/lib/dates';

type Form = Omit<Party, 'id' | 'balance'>;

const empty: Form = {
  code: '', kind: 'company', name: '', is_customer: true, is_supplier: false, is_other: false, national_id: '', economic_code: '',
  registration_number: '', phone: '', mobile: '', email: '', address: '', postal_code: '', bank_name: '', bank_account_number: '',
  iban: '', notes: '', receivable_account_id: null, payable_account_id: null, is_active: true,
};

/** Create or edit a customer/supplier. `role` presets the role for quick creation. */
export function PartyDialog({ party, role, onClose, onSaved }: { party?: Party; role?: 'customer' | 'supplier'; onClose: () => void; onSaved: (p: Party) => void }) {
  const { t } = useI18n();
  const init: Form = party ? { ...party } : { ...empty, is_customer: role !== 'supplier', is_supplier: role === 'supplier' };
  const { values: v, set, errors, setErrors } = useFormState(init);
  const { run, pending, error } = useSubmit(setErrors);
  const save = async () => {
    const p = await run(() => (party ? api.put<Party>(`/parties/${party.id}`, v) : api.post<Party>('/parties', v)), t('common.saved'));
    if (p) onSaved(p);
  };
  const digits = (k: keyof Form) => (e: React.ChangeEvent<HTMLInputElement>) => set(k, normalizeDigits(e.target.value) as never);
  return (
    <Dialog
      wide
      title={party ? t('parties.editParty') : t('parties.newParty')}
      onClose={onClose}
      footer={<><Button onClick={onClose}>{t('common.cancel')}</Button><Button variant="accent" pending={pending} onClick={save}>{t('common.save')}</Button></>}
    >
      <form className="stack" onSubmit={(e) => { e.preventDefault(); void save(); }}>
        {error && <Alert>{error}</Alert>}
        <div className="form-grid">
          <Field label={t('common.name')} required error={errors.name} className="span-2"><Input value={v.name} onChange={(e) => set('name', e.target.value)} /></Field>
          <Field label={t('common.code')} error={errors.code} help={!party ? t('products.skuHelp') : undefined}><Input value={v.code} onChange={(e) => set('code', e.target.value)} /></Field>
          <Field label={t('parties.kind')}>
            <Select value={v.kind} onChange={(e) => set('kind', e.target.value as Form['kind'])}>
              <option value="company">{t('parties.company')}</option>
              <option value="person">{t('parties.person')}</option>
            </Select>
          </Field>
          <div className="span-all row" style={{ gap: 20 }}>
            <span className="field-label">{t('parties.roles')}</span>
            <Checkbox label={t('parties.customer')} checked={v.is_customer} onChange={(x) => set('is_customer', x)} />
            <Checkbox label={t('parties.supplier')} checked={v.is_supplier} onChange={(x) => set('is_supplier', x)} />
            <Checkbox label={t('parties.other')} checked={v.is_other} onChange={(x) => set('is_other', x)} />
            {errors.is_customer && <span className="field-error">{t('validation.role_required')}</span>}
          </div>
        </div>
        <h3 className="form-section-title">{t('parties.identity')}</h3>
        <div className="form-grid">
          <Field label={t('parties.nationalId')} error={errors.national_id}><Input value={v.national_id} onChange={digits('national_id')} inputMode="numeric" className="num" /></Field>
          <Field label={t('parties.economicCode')} error={errors.economic_code}><Input value={v.economic_code} onChange={digits('economic_code')} inputMode="numeric" className="num" /></Field>
          <Field label={t('parties.registrationNumber')}><Input value={v.registration_number} onChange={digits('registration_number')} /></Field>
        </div>
        <h3 className="form-section-title">{t('parties.contact')}</h3>
        <div className="form-grid">
          <Field label={t('parties.mobile')}><Input value={v.mobile} onChange={digits('mobile')} inputMode="tel" dir="ltr" /></Field>
          <Field label={t('parties.phone')}><Input value={v.phone} onChange={digits('phone')} inputMode="tel" dir="ltr" /></Field>
          <Field label={t('parties.email')} error={errors.email}><Input value={v.email} onChange={(e) => set('email', e.target.value)} dir="ltr" /></Field>
          <Field label={t('parties.postalCode')}><Input value={v.postal_code} onChange={digits('postal_code')} /></Field>
          <Field label={t('parties.address')} className="span-all"><Input value={v.address} onChange={(e) => set('address', e.target.value)} /></Field>
        </div>
        <h3 className="form-section-title">{t('parties.bankInfo')}</h3>
        <div className="form-grid">
          <Field label={t('parties.bankName')}><Input value={v.bank_name} onChange={(e) => set('bank_name', e.target.value)} /></Field>
          <Field label={t('parties.bankAccountNumber')}><Input value={v.bank_account_number} onChange={digits('bank_account_number')} dir="ltr" /></Field>
          <Field label={t('parties.iban')} error={errors.iban}><Input value={v.iban} onChange={(e) => set('iban', normalizeDigits(e.target.value).toUpperCase())} dir="ltr" placeholder="IR…" /></Field>
        </div>
        <h3 className="form-section-title">{t('parties.accounting')}</h3>
        <div className="form-grid">
          <Field label={t('parties.receivableAccount')} help={t('parties.defaultAccount')} error={errors.receivable_account_id}>
            <AccountPicker value={v.receivable_account_id} onChange={(id) => set('receivable_account_id', id)} filter={(a) => a.type === 'asset'} />
          </Field>
          <Field label={t('parties.payableAccount')} help={t('parties.defaultAccount')} error={errors.payable_account_id}>
            <AccountPicker value={v.payable_account_id} onChange={(id) => set('payable_account_id', id)} filter={(a) => a.type === 'liability'} />
          </Field>
          {party && <div style={{ alignSelf: 'end' }}><Checkbox label={t('common.isActive')} checked={v.is_active} onChange={(x) => set('is_active', x)} /></div>}
          <Field label={t('common.notes')} className="span-all"><TextArea value={v.notes} onChange={(e) => set('notes', e.target.value)} /></Field>
        </div>
        <button type="submit" hidden />
      </form>
    </Dialog>
  );
}
