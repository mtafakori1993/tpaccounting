import { useState } from 'react';
import { ApiError } from '@/api/client';
import { useErrorMessage } from '@/i18n/errors';
import { useToast } from '@/components/ui/Toast';

/**
 * Runs a mutation, routing validation errors to fields and other errors to a
 * banner message. Returns true on success.
 */
export function useSubmit(setFieldErrors?: (e: Record<string, string>) => void) {
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const msg = useErrorMessage();
  const toast = useToast();
  const run = async <R,>(fn: () => Promise<R>, success?: string): Promise<R | undefined> => {
    setPending(true);
    setError(null);
    try {
      const r = await fn();
      setFieldErrors?.({});
      if (success) toast.success(success);
      return r;
    } catch (e) {
      if (e instanceof ApiError && Object.keys(e.fields).length && setFieldErrors) setFieldErrors(e.fields);
      setError(msg(e));
      return undefined;
    } finally {
      setPending(false);
    }
  };
  return { run, pending, error, setError };
}
