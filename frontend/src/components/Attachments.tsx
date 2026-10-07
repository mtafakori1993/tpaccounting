import { useRef } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Paperclip, Trash2 } from 'lucide-react';
import { api } from '@/api/client';
import type { Attachment } from '@/api/types';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { useSubmit } from '@/lib/useSubmit';
import { Button, IconButton } from './ui/Button';
import { Alert, Panel } from './ui/Misc';

/** File attachments for any supported entity. */
export function Attachments({ entity, id, canEdit }: { entity: string; id: number; canEdit: boolean }) {
  const { t } = useI18n();
  const f = useFormat();
  const qc = useQueryClient();
  const input = useRef<HTMLInputElement>(null);
  const key = ['attachments', entity, id];
  const q = useQuery({ queryKey: key, queryFn: () => api.get<Attachment[]>('/attachments', { entity_type: entity, entity_id: id }) });
  const { run, pending, error } = useSubmit();

  const upload = async (file: File) => {
    const fd = new FormData();
    fd.set('entity_type', entity);
    fd.set('entity_id', String(id));
    fd.set('file', file);
    await run(() => api.post('/attachments', fd), t('common.saved'));
    void qc.invalidateQueries({ queryKey: key });
  };
  const size = (b: number) => (b > 1 << 20 ? `${f.qty((b / (1 << 20)).toFixed(1))} MB` : `${f.count(Math.ceil(b / 1024))} KB`);

  return (
    <Panel
      title={t('common.attachments')}
      actions={canEdit && (
        <>
          <input ref={input} type="file" hidden onChange={(e) => { const file = e.target.files?.[0]; if (file) void upload(file); e.target.value = ''; }} />
          <Button size="sm" icon={<Paperclip size={14} />} pending={pending} onClick={() => input.current?.click()}>{t('common.attach')}</Button>
        </>
      )}
    >
      <div className="stack-sm">
        {error && <Alert>{error}</Alert>}
        {q.data?.length === 0 && <p className="muted">{t('common.noAttachments')}</p>}
        {q.data?.map((a) => (
          <div key={a.id} className="row-between">
            <a href={`/api/v1/attachments/${a.id}/download`} target="_blank" rel="noreferrer" className="grow" style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
              {a.file_name}
            </a>
            <span className="muted num" style={{ fontSize: 12 }}>{size(a.size_bytes)}</span>
            {canEdit && (
              <IconButton size="sm" label={t('common.remove')} icon={<Trash2 size={14} />} onClick={() => run(() => api.del(`/attachments/${a.id}`)).then(() => qc.invalidateQueries({ queryKey: key }))} />
            )}
          </div>
        ))}
      </div>
    </Panel>
  );
}
