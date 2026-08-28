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

// 可选注入的查询函数：租户页走 auditApi.listEvents/getEvent，平台页走
// auditApi.listPlatformEvents/getPlatformEvent。用 ref 缓存，避免调用方传入
// 内联对象导致每次渲染重建 listEvents/getEvent 引用、连带重建 load/openDetail。
export interface AuditListPageFetchers {
  listEvents?: typeof auditApi.listEvents;
  getEvent?: typeof auditApi.getEvent;
}

export const useAuditListPage = (fetchers?: AuditListPageFetchers) => {
  const [events, setEvents] = useState<ResourceChangeAudit[]>([]);
  const [loading, setLoading] = useState(true);
  const [filters, setFilters] = useState<AuditFilters>(EMPTY_FILTERS);
  const [detailId, setDetailId] = useState<string | null>(null);
  const [detailEvent, setDetailEvent] = useState<ResourceChangeAudit | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  // 请求序号：快速翻页/切换筛选时丢弃过期响应，避免旧数据覆盖新数据。
  const requestSeqRef = useRef(0);
  const fetchersRef = useRef(fetchers);
  const listEvents = fetchersRef.current?.listEvents ?? auditApi.listEvents;
  const getEvent = fetchersRef.current?.getEvent ?? auditApi.getEvent;
  const { current: page, pageSize, total, setTotal, onChange, pageSizeOptions } = usePagination();

  const load = useCallback(async (nextPage: number, nextPageSize: number, nextFilters: AuditFilters) => {
    const seq = ++requestSeqRef.current;
    setLoading(true);
    try {
      const pageData = await listEvents({ ...nextFilters, page: nextPage, pageSize: nextPageSize });
      if (seq !== requestSeqRef.current) return;
      setEvents(pageData.events);
      setTotal(pageData.total);
    } catch (err) {
      if (seq !== requestSeqRef.current) return;
      message.error({ content: (err as RequestError).response?.data?.error || '加载审计记录失败', duration: 3 });
    } finally {
      if (seq === requestSeqRef.current) setLoading(false);
    }
  }, [setTotal, listEvents]);

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
      const event = await getEvent(id);
      setDetailEvent(event);
    } catch (err) {
      message.error({ content: (err as RequestError).response?.data?.error || '加载审计详情失败', duration: 3 });
    } finally {
      setDetailLoading(false);
    }
  }, [getEvent]);

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
