import { Typography } from 'antd';

import { AgentResultModal } from '../components/AgentResultModal';
import { AgentTaskModal } from '../components/AgentTaskModal';
import { AgentsListFilters } from '../components/AgentsListFilters';
import { AgentsListGrid } from '../components/AgentsListGrid';
import { useAgentsListPage } from '../hooks/useAgentsListPage';

import { useTenantRole } from '@/modules/iam';

const { Title } = Typography;

export const AgentsListPage = () => {
  const {
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
  } = useAgentsListPage();
  const { isAdmin } = useTenantRole();

  const closeResult = () => {
    setShowResultModal(false);
    setExecutingAgent(null);
    setExecutionResult(null);
  };

  return (
    <div>
      <div
        className="responsive-page-header"
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          marginBottom: 24,
        }}
      >
        <Title level={4} style={{ margin: 0 }}>
          Agent 列表
        </Title>
        <AgentsListFilters
          searchText={searchText}
          onSearchChange={setSearchText}
          onCreate={() => navigate('/agents/create')}
          canManage={isAdmin}
        />
      </div>

      <AgentsListGrid
        agents={filteredAgents}
        loading={loading}
        searchText={searchText}
        onExecute={(a) => {
          setExecutingAgent(a);
          setTaskQuery('');
          setTaskContext('{}');
          setTaskModalVisible(true);
        }}
        onEdit={(a) => navigate(`/agents/${a.id}/edit`)}
        onDelete={handleDeleteAgent}
        onCreate={() => navigate('/agents/create')}
        canManage={isAdmin}
      />

      <AgentTaskModal
        agent={executingAgent}
        open={taskModalVisible}
        loading={executing}
        taskQuery={taskQuery}
        taskContext={taskContext}
        onQueryChange={setTaskQuery}
        onContextChange={setTaskContext}
        onSubmit={handleTaskSubmit}
        onCancel={() => setTaskModalVisible(false)}
      />

      <AgentResultModal
        agent={executingAgent}
        open={showResultModal}
        result={executionResult}
        onClose={closeResult}
      />
    </div>
  );
};
