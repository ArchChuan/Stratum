import { message } from 'antd';
import { useCallback, useEffect, useState } from 'react';

import { proposalApi } from '../api/proposal.api';
import {
  TERMINAL_PROPOSAL_STATUSES,
  type ProposalPayload,
  type ResourceChangeProposal,
} from '../model/proposal';

const errorContent = (error: unknown) => {
  const value = error as { response?: { data?: { error?: string } } };
  return value.response?.data?.error || '操作失败';
};

const preserveEvents = (current: ResourceChangeProposal | undefined, next: ResourceChangeProposal) => ({
  ...next,
  events: next.events.length > 0 ? next.events : (current?.events ?? []),
});

export const useResourceChangeProposal = (id: string) => {
  const [proposal, setProposal] = useState<ResourceChangeProposal>();
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [canceling, setCanceling] = useState(false);

  const load = useCallback(async () => {
    if (!id) return;
    setLoading(true);
    try { setProposal(await proposalApi.get(id)); }
    catch (error) { message.error({ content: errorContent(error), duration: 0 }); }
    finally { setLoading(false); }
  }, [id]);

  useEffect(() => {
    let cancelled = false;
    const run = async () => {
      if (!id) { setLoading(false); return; }
      setLoading(true);
      try {
        const value = await proposalApi.get(id);
        if (!cancelled) setProposal(value);
      } catch (error) {
        if (!cancelled) message.error({ content: errorContent(error), duration: 0 });
      } finally {
        if (!cancelled) setLoading(false);
      }
    };
    void run();
    return () => { cancelled = true; };
  }, [id]);

  const mutable = proposal?.status === 'ready_for_review' && !TERMINAL_PROPOSAL_STATUSES.has(proposal.status);
  const saveDraft = async (payload: ProposalPayload) => {
    if (!proposal || !mutable || saving) return;
    setSaving(true);
    try {
      const value = await proposalApi.update(proposal.id, payload);
      setProposal((current) => preserveEvents(current, value));
      message.success({ content: '提案已保存', duration: 2 });
    } catch (error) { message.error({ content: errorContent(error), duration: 0 }); }
    finally { setSaving(false); }
  };
  const confirm = async () => {
    if (!proposal || !mutable || confirming) return;
    setConfirming(true);
    try {
      const value = await proposalApi.confirm(proposal.id);
      setProposal((current) => preserveEvents(current, value));
      message.success({ content: '变更已应用', duration: 2 });
    } catch (error) { message.error({ content: errorContent(error), duration: 0 }); void load(); }
    finally { setConfirming(false); }
  };
  const cancel = async () => {
    if (!proposal || !mutable || canceling) return;
    setCanceling(true);
    try {
      await proposalApi.cancel(proposal.id);
      message.success({ content: '提案已取消', duration: 2 });
      void load();
    } catch (error) { message.error({ content: errorContent(error), duration: 0 }); }
    finally { setCanceling(false); }
  };

  return { proposal, events: proposal?.events ?? [], loading, saving, confirming, canceling, saveDraft, confirm, cancel };
};
