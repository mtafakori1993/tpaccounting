// Shared cached lookups used by pickers and forms across the app.
import { useQuery } from '@tanstack/react-query';
import { api } from './client';
import type { Account, Category, CostCenter, ExpenseCategory, TreasuryAccount, Warehouse } from './types';

const lookup = { staleTime: 60_000 };

export const useAccounts = () => useQuery({ queryKey: ['accounts', 'all'], queryFn: () => api.get<Account[]>('/accounts'), ...lookup });
export const useCostCenters = () => useQuery({ queryKey: ['cost-centers'], queryFn: () => api.get<CostCenter[]>('/cost-centers'), ...lookup });
export const useWarehouses = () => useQuery({ queryKey: ['warehouses'], queryFn: () => api.get<Warehouse[]>('/inventory/warehouses'), ...lookup });
export const useTreasuryAccounts = () => useQuery({ queryKey: ['treasury-accounts'], queryFn: () => api.get<TreasuryAccount[]>('/treasury/accounts'), ...lookup });
export const useProductCategories = () => useQuery({ queryKey: ['product-categories'], queryFn: () => api.get<Category[]>('/products/categories'), ...lookup });
export const useExpenseCategories = () => useQuery({ queryKey: ['expense-categories'], queryFn: () => api.get<ExpenseCategory[]>('/expenses/categories'), ...lookup });
