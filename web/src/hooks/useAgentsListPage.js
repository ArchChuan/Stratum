import { useState, useEffect, useCallback } from 'react';
import { message } from 'antd';
import { useNavigate } from 'react-router-dom';
import { getAllAgents, executeAgent, deleteAgent } from '../services/api';

const useAgentsListPage = () => {
  const navigate = useNavigate();
  const [agents, setAgents] = useState([]);
  const [loading, setLoading] = useState(true);
  const [searchText, setSearchText] = useState('');
  const [executingAgent, setExecutingAgent] = useState(null);
  const [executionResult, setExecutionResult] = useState(null);
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
        const res = await getAllAgents();
        if (!cancelled) setAgents(res.data.agents || []);
      } catch {
        if (!cancelled) message.error('获取 Agent 列表失败');
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => { cancelled = true; };
  }, []);

  const handleTaskSubmit = useCallback(async () => {
    if (!executingAgent || !taskQuery.trim()) { message.warning('请输入任务内容'); return; }
    setExecuting(true);
    let ctx = {};
    try { ctx = JSON.parse(taskContext || '{}'); } catch { /* ignore */ }
    try {
      const res = await executeAgent(executingAgent.id, { query: taskQuery, context: ctx, variables: {} });
      setExecutionResult(res.data);
      setTaskModalVisible(false);
      setShowResultModal(true);
    } catch (err) {
      setExecutionResult({ error: err.response?.data?.error || err.message });
      setTaskModalVisible(false);
      setShowResultModal(true);
    } finally {
      setExecuting(false);
    }
  }, [executingAgent, taskQuery, taskContext]);

  const handleDeleteAgent = useCallback(async (agentId) => {
    try {
      await deleteAgent(agentId);
      message.success('Agent 已删除');
      setAgents(prev => prev.filter(a => a.id !== agentId));
    } catch (err) {
      if (err.response?.status !== 403) {
        message.error(err.response?.data?.error || '删除失败');
      }
    }
  }, []);

  const filteredAgents = agents.filter(a =>
    a.name.toLowerCase().includes(searchText.toLowerCase()) ||
    (a.description || '').toLowerCase().includes(searchText.toLowerCase())
  );

  return {
    agents: filteredAgents, loading, searchText, setSearchText,
    executingAgent, setExecutingAgent,
    executionResult, setExecutionResult,
    showResultModal, setShowResultModal,
    taskModalVisible, setTaskModalVisible,
    taskQuery, setTaskQuery,
    taskContext, setTaskContext,
    executing, navigate,
    handleTaskSubmit, handleDeleteAgent,
  };
};

export default useAgentsListPage;
