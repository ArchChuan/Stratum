import { useCallback, useRef, useState } from 'react';

import { evaluationApi } from '../api/evaluation.api';
import type { ResourceKind, TimelineEvent } from '../model/evaluation';

interface TimelineResource { resource_kind: ResourceKind; resource_id: string }

export const useEvaluationTimeline = () => {
  const [events, setEvents] = useState<TimelineEvent[]>([]);
  const [open, setOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const generationRef = useRef(0);
  const resourceKeyRef = useRef('');

  const openTimeline = useCallback(async (resource: TimelineResource) => {
    const generation = generationRef.current + 1;
    const resourceKey = `${resource.resource_kind}:${resource.resource_id}`;
    generationRef.current = generation; resourceKeyRef.current = resourceKey;
    setOpen(true); setLoading(true); setError(''); setEvents([]);
    try {
      const page = await evaluationApi.getTimeline(resource.resource_kind, resource.resource_id);
      if (generationRef.current === generation && resourceKeyRef.current === resourceKey) setEvents(page.items);
    } catch {
      if (generationRef.current === generation && resourceKeyRef.current === resourceKey) {
        setEvents([]); setError('加载资源时间线失败');
      }
    } finally {
      if (generationRef.current === generation && resourceKeyRef.current === resourceKey) setLoading(false);
    }
  }, []);
  const closeTimeline = useCallback(() => {
    generationRef.current += 1; resourceKeyRef.current = '';
    setOpen(false); setLoading(false); setError(''); setEvents([]);
  }, []);
  return { events, open, loading, error, openTimeline, closeTimeline };
};
