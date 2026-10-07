import { useEffect, useRef, useState, type ReactNode } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Eye, Printer, RotateCcw } from 'lucide-react';
import { api } from '@/api/client';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useErrorMessage } from '@/i18n/errors';
import { Button } from '@/components/ui/Button';
import { Checkbox, Field, Input, Select, TextArea } from '@/components/ui/Field';
import { Alert, LoadingBlock, PageHeader, Panel, Spinner, Tabs } from '@/components/ui/Misc';
import { useDebounced } from '@/lib/hooks';
import { useSubmit } from '@/lib/useSubmit';
import { fetchPdf, printBlob } from '@/lib/pdf';

export interface LetterheadSettings {
  enabled: boolean;
  for_print: boolean;
  header: {
    enabled: boolean; mode: 'every' | 'first'; height_mm: number; top_margin_mm: number; title: string; subtitle: string;
    logo_source: 'letterhead' | 'company'; logo_width_mm: number; logo_position: 'right' | 'left'; show_fields: boolean;
  };
  footer: {
    enabled: boolean; text: string; font: 'vazir' | 'vazir-fa-digits'; size_pt: number; color: string; value_color: string;
    align: 'right' | 'center' | 'left'; separator: boolean; height_mm: number; offset_mm: number; page_numbers: boolean; page_number_text: string;
  };
  page: { size: 'A4'; orientation: 'portrait' | 'landscape'; margin_top_mm: number; margin_bottom_mm: number; margin_left_mm: number; margin_right_mm: number };
}

interface Response { settings: LetterheadSettings; defaults: LetterheadSettings; variables: string[] }

type Section = 'general' | 'header' | 'footer' | 'page';

export function LetterheadPage() {
  const { t, lang } = useI18n();
  const { can } = useAuth();
  const msg = useErrorMessage();
  const qc = useQueryClient();
  const editable = can('settings.edit');
  const q = useQuery({ queryKey: ['letterhead'], queryFn: () => api.get<Response>('/settings/letterhead') });
  const [s, setS] = useState<LetterheadSettings | null>(null);
  const [section, setSection] = useState<Section>('general');
  const [pages, setPages] = useState(1);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const save = useSubmit(setErrors);

  useEffect(() => {
    if (q.data && !s) setS(q.data.settings);
  }, [q.data, s]);

  // Live preview rendered by the same engine as every real document.
  const debounced = useDebounced(s, 600);
  const [preview, setPreview] = useState<{ url: string; blob: Blob } | null>(null);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const [rendering, setRendering] = useState(false);
  const seq = useRef(0);
  useEffect(() => {
    if (!debounced) return;
    const n = ++seq.current;
    setRendering(true);
    fetchPdf('/settings/letterhead/preview', { settings: debounced, lang, pages })
      .then((blob) => {
        if (n !== seq.current) return;
        setPreviewError(null);
        setPreview((old) => {
          if (old) URL.revokeObjectURL(old.url);
          return { url: URL.createObjectURL(blob), blob };
        });
      })
      .catch((e) => n === seq.current && setPreviewError(msg(e)))
      .finally(() => n === seq.current && setRendering(false));
  }, [debounced, lang, pages]);

  if (!s || !q.data) return <LoadingBlock />;
  const dirty = JSON.stringify(s) !== JSON.stringify(q.data.settings);
  const set = <K extends keyof LetterheadSettings>(k: K, v: LetterheadSettings[K]) => setS({ ...s, [k]: v });
  const h = <K extends keyof LetterheadSettings['header']>(k: K, v: LetterheadSettings['header'][K]) => setS({ ...s, header: { ...s.header, [k]: v } });
  const ft = <K extends keyof LetterheadSettings['footer']>(k: K, v: LetterheadSettings['footer'][K]) => setS({ ...s, footer: { ...s.footer, [k]: v } });
  const pg = <K extends keyof LetterheadSettings['page']>(k: K, v: LetterheadSettings['page'][K]) => setS({ ...s, page: { ...s.page, [k]: v } });
  const num = (v: number, on: (n: number) => void, step = 0.5) => (
    <Input type="number" step={step} value={v} disabled={!editable} className="num" onChange={(e) => on(Number(e.target.value))} dir="ltr" />
  );
  const err = (k: string) => errors[k];

  const color = (value: string, on: (v: string) => void) => (
    <div className="row" style={{ flexWrap: 'nowrap' }}>
      <input type="color" value={value} disabled={!editable} onChange={(e) => on(e.target.value.toUpperCase())} style={{ width: 40, height: 34, padding: 2, border: '1px solid var(--color-border-strong)', borderRadius: 4, background: 'var(--color-surface)' }} aria-label={value} />
      <Input value={value} disabled={!editable} onChange={(e) => on(e.target.value)} dir="ltr" className="num" maxLength={7} />
    </div>
  );

  const sections: Record<Section, ReactNode> = {
    general: (
      <div className="stack">
        <Checkbox label={t('settings.lh.enabled')} checked={s.enabled} disabled={!editable} onChange={(v) => set('enabled', v)} />
        <Checkbox label={t('settings.lh.forPrint')} checked={s.for_print} disabled={!editable} onChange={(v) => set('for_print', v)} />
      </div>
    ),
    header: (
      <div className="form-grid">
        <div className="span-all"><Checkbox label={t('settings.lh.headerEnabled')} checked={s.header.enabled} disabled={!editable} onChange={(v) => h('enabled', v)} /></div>
        <Field label={t('settings.lh.titleText')} className="span-all" error={err('header.title')}><Input value={s.header.title} disabled={!editable} onChange={(e) => h('title', e.target.value)} /></Field>
        <Field label={t('settings.lh.subtitleText')} className="span-all" error={err('header.subtitle')}><Input value={s.header.subtitle} disabled={!editable} onChange={(e) => h('subtitle', e.target.value)} dir="ltr" /></Field>
        <Field label={t('settings.lh.mode')}>
          <Select value={s.header.mode} disabled={!editable} onChange={(e) => h('mode', e.target.value as 'every')}>
            <option value="every">{t('settings.lh.modeEvery')}</option>
            <option value="first">{t('settings.lh.modeFirst')}</option>
          </Select>
        </Field>
        <Field label={t('settings.lh.logo')}>
          <Select value={s.header.logo_source} disabled={!editable} onChange={(e) => h('logo_source', e.target.value as 'letterhead')}>
            <option value="letterhead">{t('settings.lh.logoLetterhead')}</option>
            <option value="company">{t('settings.lh.logoCompany')}</option>
          </Select>
        </Field>
        <Field label={t('settings.lh.logoPosition')}>
          <Select value={s.header.logo_position} disabled={!editable} onChange={(e) => h('logo_position', e.target.value as 'right')}>
            <option value="right">{t('settings.lh.right')}</option>
            <option value="left">{t('settings.lh.left')}</option>
          </Select>
        </Field>
        <Field label={t('settings.lh.logoWidth')} error={err('header.logo_width_mm')}>{num(s.header.logo_width_mm, (v) => h('logo_width_mm', v))}</Field>
        <Field label={t('settings.lh.height')} error={err('header.height_mm')}>{num(s.header.height_mm, (v) => h('height_mm', v))}</Field>
        <Field label={t('settings.lh.topMargin')} error={err('header.top_margin_mm')}>{num(s.header.top_margin_mm, (v) => h('top_margin_mm', v))}</Field>
        <div className="span-all"><Checkbox label={t('settings.lh.showFields')} checked={s.header.show_fields} disabled={!editable} onChange={(v) => h('show_fields', v)} /></div>
      </div>
    ),
    footer: (
      <div className="form-grid">
        <div className="span-all"><Checkbox label={t('settings.lh.footerEnabled')} checked={s.footer.enabled} disabled={!editable} onChange={(v) => ft('enabled', v)} /></div>
        <Field label={t('settings.lh.footerText')} className="span-all" error={err('footer.text')} help={t('settings.lh.footerTextHelp')}>
          <TextArea value={s.footer.text} disabled={!editable} onChange={(e) => ft('text', e.target.value)} rows={2} />
        </Field>
        <div className="span-all field-help">
          {t('settings.lh.variables')}:{' '}
          {q.data.variables.map((v) => (
            <button key={v} type="button" className="btn btn-quiet btn-sm" disabled={!editable} onClick={() => ft('text', `${s.footer.text}{${v}}`)} dir="ltr">{`{${v}}`}</button>
          ))}
        </div>
        <Field label={t('settings.lh.font')}>
          <Select value={s.footer.font} disabled={!editable} onChange={(e) => ft('font', e.target.value as 'vazir')}>
            <option value="vazir-fa-digits">{t('settings.lh.fontFa')}</option>
            <option value="vazir">{t('settings.lh.fontLatin')}</option>
          </Select>
        </Field>
        <Field label={t('settings.lh.size')} error={err('footer.size_pt')}>
          <Select value={String(s.footer.size_pt)} disabled={!editable} onChange={(e) => ft('size_pt', Number(e.target.value))}>
            {[7, 7.5, 8, 8.5, 9, 9.5, 10, 11, 12].map((n) => <option key={n} value={n}>{n}</option>)}
          </Select>
        </Field>
        <Field label={t('settings.lh.align')}>
          <Select value={s.footer.align} disabled={!editable} onChange={(e) => ft('align', e.target.value as 'center')}>
            <option value="right">{t('settings.lh.right')}</option>
            <option value="center">{t('settings.lh.center')}</option>
            <option value="left">{t('settings.lh.left')}</option>
          </Select>
        </Field>
        <Field label={t('settings.lh.color')} error={err('footer.color')}>{color(s.footer.color, (v) => ft('color', v))}</Field>
        <Field label={t('settings.lh.valueColor')} error={err('footer.value_color')}>{color(s.footer.value_color, (v) => ft('value_color', v))}</Field>
        <Field label={t('settings.lh.footerHeight')} error={err('footer.height_mm')}>{num(s.footer.height_mm, (v) => ft('height_mm', v))}</Field>
        <Field label={t('settings.lh.footerOffset')} error={err('footer.offset_mm')}>{num(s.footer.offset_mm, (v) => ft('offset_mm', v))}</Field>
        <div className="span-all"><Checkbox label={t('settings.lh.separator')} checked={s.footer.separator} disabled={!editable} onChange={(v) => ft('separator', v)} /></div>
        <div className="span-all"><Checkbox label={t('settings.lh.pageNumbers')} checked={s.footer.page_numbers} disabled={!editable} onChange={(v) => ft('page_numbers', v)} /></div>
        {s.footer.page_numbers && (
          <Field label={t('settings.lh.pageNumberText')} className="span-all" error={err('footer.page_number_text')}>
            <Input value={s.footer.page_number_text} disabled={!editable} onChange={(e) => ft('page_number_text', e.target.value)} />
          </Field>
        )}
      </div>
    ),
    page: (
      <div className="form-grid">
        <Field label={t('settings.lh.paper')}><Select value="A4" disabled><option value="A4">A4 — 210 × 297 mm</option></Select></Field>
        <Field label={t('settings.lh.orientation')}>
          <Select value={s.page.orientation} disabled={!editable} onChange={(e) => pg('orientation', e.target.value as 'portrait')}>
            <option value="portrait">{t('settings.lh.portrait')}</option>
            <option value="landscape">{t('settings.lh.landscape')}</option>
          </Select>
        </Field>
        <Field label={t('settings.lh.marginTop')} error={err('page.margin_top_mm')}>{num(s.page.margin_top_mm, (v) => pg('margin_top_mm', v))}</Field>
        <Field label={t('settings.lh.marginBottom')} error={err('page.margin_bottom_mm')}>{num(s.page.margin_bottom_mm, (v) => pg('margin_bottom_mm', v))}</Field>
        <Field label={t('settings.lh.marginRight')} error={err('page.margin_right_mm')}>{num(s.page.margin_right_mm, (v) => pg('margin_right_mm', v))}</Field>
        <Field label={t('settings.lh.marginLeft')} error={err('page.margin_left_mm')}>{num(s.page.margin_left_mm, (v) => pg('margin_left_mm', v))}</Field>
      </div>
    ),
  };

  const sectionOf = (k: string) => (k.split('.')[0] as Section);
  const firstError = Object.keys(errors)[0];

  return (
    <div className="page">
      <PageHeader
        title={t('settings.lh.title')}
        subtitle={t('settings.lh.intro')}
        actions={<>
          <Button icon={<Eye size={16} />} disabled={!preview} onClick={() => preview && window.open(preview.url, '_blank')}>{t('settings.lh.previewPdf')}</Button>
          <Button icon={<Printer size={16} />} disabled={!preview} onClick={() => preview && printBlob(preview.blob)}>{t('settings.lh.testPrint')}</Button>
          {editable && <Button icon={<RotateCcw size={16} />} onClick={() => setS(q.data.defaults)}>{t('settings.lh.reset')}</Button>}
          {editable && (
            <Button variant="accent" disabled={!dirty} pending={save.pending} onClick={async () => {
              const r = await save.run(() => api.put<Response>('/settings/letterhead', s), t('common.saved'));
              if (r) {
                qc.setQueryData(['letterhead'], r);
                setS(r.settings);
              }
            }}>{t('common.save')}</Button>
          )}
        </>}
      />
      <div className="lh-layout">
        <div className="stack">
          {!editable && <Alert kind="info">{t('settings.lh.readOnly')}</Alert>}
          {dirty && <Alert kind="warning">{t('settings.lh.unsaved')}</Alert>}
          {save.error && <Alert>{save.error}{firstError && ` (${t(`settings.lh.${sectionOf(firstError)}` as 'settings.lh.page')})`}</Alert>}
          <Panel bodyless>
            <Tabs<Section>
              value={section}
              onChange={setSection}
              tabs={(['general', 'header', 'footer', 'page'] as const).map((k) => ({ key: k, label: t(`settings.lh.${k}`) }))}
            />
            <div className="panel-body">{sections[section]}</div>
          </Panel>
        </div>
        <Panel bodyless title={t('settings.lh.preview')} className="lh-preview-panel" actions={
          <div className="row">
            {rendering && <span className="row muted" style={{ fontSize: 12 }}><Spinner /> {t('settings.lh.updating')}</span>}
            <label className="field-label" htmlFor="lh-pages">{t('settings.lh.previewPages')}</label>
            <Select id="lh-pages" value={pages} onChange={(e) => setPages(Number(e.target.value))} style={{ width: 70 }}>
              {[1, 2, 3].map((n) => <option key={n} value={n}>{n}</option>)}
            </Select>
          </div>
        }>
          {previewError && <div style={{ padding: 16 }}><Alert>{previewError}</Alert></div>}
          {preview ? (
            <iframe className="lh-preview" title={t('settings.lh.preview')} src={`${preview.url}#toolbar=0&navpanes=0&view=FitH`} />
          ) : (
            <LoadingBlock />
          )}
        </Panel>
      </div>
    </div>
  );
}
