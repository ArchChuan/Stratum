import { message } from 'antd';
import { useCallback, useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';

import { agentApi } from '../api/agent.api';
import type { Agent, AgentExecutionResult } from '../model/agent';

import { extractErrorMessage } from '@/shared/lib';

export const useAgentsListPage = () => {
  const navigate = useNavigate();
  const [agents, setAgents] = useState<Agent[]>([]);
  const [loading, setLoading] = useState(true);
  const [searchText, setSearchText] = useState('');
  const [executingAgent, setExecutingAgent] = useState<Agent | null>(null);
  const [executionResult, setExecutionResult] = useState<AgentExecutionResult | null>(null);
  const [showResultModal, setShowResultModal] = useState(false);
  const [taskModalVisible, setTaskModalVisible] = useState(false);
  const [taskQuery, setTaskQuery] = useState('');
  const [taskContext, setTaskContext] = useState('{}');
  const [executing, setExecuting] = useState(false);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      setLoading(true);
      try {
        const list = await agentApi.list();
        if (!cancelled) setAgents(list);
      } catch {
        if (!cancelled) message.error('获取 Agent 列表失败');
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  const handleTaskSubmit = useCallback(async () => {
    if (!executingAgent || !taskQuery.trim()) {
      message.warning('请输入任务内容');
      return;
    }
    setExecuting(true);
    let ctx: Record<string, unknown> = {};
    try {
      ctx = JSON.parse(taskContext || '{}');
    } catch {
      /* ignore */
    }
    try {
      const res = await agentApi.execute(executingAgent.id, {
        query: taskQuery,
        context: ctx,
        variables: {},
      });
      setExecutionResult(res.data as AgentExecutionResult);
      setTaskModalVisible(false);
      setShowResultModal(true);
    } catch (err) {
      setExecutionResult({ error: extractErrorMessage(err) });
      setTaskModalVisible(false);
      setShowResultModal(true);
    } finally {
      setExecuting(false);
    }
  }, [executingAgent, taskQuery, taskContext]);

  const handleDeleteAgent = useCallback(async (agentId: string) => {
    try {
      await agentApi.delete(agentId);
      message.success('Agent 已删除');
      setAgents((prev) => prev.filter((a) => a.id !== agentId));
    } catch (err) {
      const status = (err as { response?: { status?: number } })?.response?.status;
      if (status !== 403) message.error(extractErrorMessage(err) || '删除失败');
    }
  }, []);

  const filteredAgents = agents.filter(
    (a) =>
      a.name.toLowerCase().includes(searchText.toLowerCase()) ||
      (a.description || '').toLowerCase().includes(searchText.toLowerCase()),
  );

  return {
    agents: filteredAgents,
    loading,
    searchText,
    setSearchText,
    executingAgent,
    setExecutingAgent,
    executionResult,
    setExecutionResult,
    showResultModal,
    setShowResultModal,
    taskModalVisible,
    setTaskModalVisible,
    taskQuery,
    setTaskQuery,
    taskContext,
    setTaskContext,
    executing,
    navigate,
    handleTaskSubmit,
    handleDeleteAgent,
  };
};
