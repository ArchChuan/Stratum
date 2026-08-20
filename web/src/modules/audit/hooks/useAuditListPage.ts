import { message } from 'antd';
import { useCallback, useEffect, useRef, useState } from 'react';

import { auditApi } from '../api/audit.api';
import type { ResourceChangeAudit } from '../model/audit';

import { usePagination } from '@/shared/hooks';

export interface AuditFilters {
  from?: string;
  to?: string;
  resourceKind?: string;
  actorName?: string;
}

interface RequestError { response?: { data?: { error?: string } } }

const EMPTY_FILTERS: AuditFilters = {};

export const useAuditListPage = () => {
  const [events, setEvents] = useState<ResourceChangeAudit[]>([]);
  const [loading, setLoading] = useState(true);
  const [filters, setFilters] = useState<AuditFilters>(EMPTY_FILTERS);
  const [detailId, setDetailId] = useState<string | null>(null);
  const [detailEvent, setDetailEvent] = useState<ResourceChangeAudit | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  // 请求序号：快速翻页/切换筛选时丢弃过期响应，避免旧数据覆盖新数据。
  const requestSeqRef = useRef(0);
  const { current: page, pageSize, total, setTotal, onChange, pageSizeOptions } = usePagination();

  const load = useCallback(async (nextPage: number, nextPageSize: number, nextFilters: AuditFilters) => {
    const seq = ++requestSeqRef.current;
    setLoading(true);
    try {
      const pageData = await auditApi.listEvents({ ...nextFilters, page: nextPage, pageSize: nextPageSize });
      if (seq !== requestSeqRef.current) return;
      setEvents(pageData.events);
      setTotal(pageData.total);
    } catch (err) {
      if (seq !== requestSeqRef.current) return;
      message.error({ content: (err as RequestError).response?.data?.error || '加载审计记录失败', duration: 3 });
    } finally {
      if (seq === requestSeqRef.current) setLoading(false);
    }
  }, [setTotal]);

  useEffect(() => {
    void load(1, pageSize, EMPTY_FILTERS);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- 仅首次加载
  }, []);

  // 筛选变化：重置回第 1 页并重新加载。
  const applyFilters = useCallback((next: AuditFilters) => {
    setFilters(next);
    void load(1, pageSize, next);
  }, [load, pageSize]);

  const handlePageChange = useCallback((nextPage: number, nextPageSize: number) => {
    onChange(nextPage, nextPageSize);
    void load(nextPage, nextPageSize, filters);
  }, [onChange, load, filters]);

  const openDetail = useCallback(async (id: string) => {
    setDetailId(id);
    setDetailEvent(null);
    setDetailLoading(true);
    try {
      const event = await auditApi.getEvent(id);
      setDetailEvent(event);
    } catch (err) {
      message.error({ content: (err as RequestError).response?.data?.error || '加载审计详情失败', duration: 3 });
    } finally {
      setDetailLoading(false);
    }
  }, []);

  const closeDetail = useCallback(() => {
    setDetailId(null);
    setDetailEvent(null);
  }, []);

  return {
    events,
    loading,
    filters,
    total,
    page,
    pageSize,
    pageSizeOptions,
    detailId,
    detailEvent,
    detailLoading,
    applyFilters,
    handlePageChange,
    openDetail,
    closeDetail,
  };
};

export default useAuditListPage;
