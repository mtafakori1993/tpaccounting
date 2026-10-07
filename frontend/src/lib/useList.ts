import { useState } from 'react';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { api, type Query } from '@/api/client';
import type { Page } from '@/api/types';
import type { Sort } from '@/components/ui/DataTable';
import { useDebounced, usePersistentState } from './hooks';

/**
 * Server-side paginated list with persisted filters and sorting.
 * `filters` keys are sent as query parameters.
 */
export function useList<T, F extends Record<string, string | number | boolean>>(key: string, path: string, defaults: F, defaultSort: Sort, pageSize = 50) {
  const [filters, setFilters] = usePersistentState<F>(`filters.${key}`, defaults);
  const [sort, setSort] = useState<Sort>(defaultSort);
  const [page, setPage] = useState(1);
  const debounced = useDebounced(filters, 250);
  const params: Query = { ...debounced, page, page_size: pageSize, sort: sort.key, order: sort.desc ? 'desc' : 'asc' };
  const query = useQuery({
    queryKey: [key, params],
    queryFn: () => api.get<Page<T>>(path, params),
    placeholderData: keepPreviousData,
  });
  return {
    query,
    filters,
    setFilter: <K extends keyof F>(k: K, v: F[K]) => {
      setFilters({ ...filters, [k]: v });
      setPage(1);
    },
    patch: (p: Partial<F>) => {
      setFilters({ ...filters, ...p });
      setPage(1);
    },
    resetFilters: () => {
      setFilters(defaults);
      setPage(1);
    },
    sort,
    setSort,
    page,
    setPage,
    pageSize,
  };
}
