'use client';

import { Grid, type PaginationProps } from 'antd';

interface UseResponsivePaginationArgs {
  current: number;
  pageSize: number;
  total: number;
  onChange: (page: number, pageSize: number) => void;
}

/**
 * Compact pager on phones (simple, no size changer/total text crowding the
 * footer); full size changer + total on md and up.
 */
export function useResponsivePagination({ current, pageSize, total, onChange }: UseResponsivePaginationArgs): PaginationProps {
  const isCompact = !Grid.useBreakpoint().md;
  if (isCompact) {
    return { current, pageSize, total, simple: true, onChange };
  }
  return {
    current,
    pageSize,
    total,
    showSizeChanger: true,
    showTotal: (count) => `共 ${count} 条`,
    onChange,
  };
}
