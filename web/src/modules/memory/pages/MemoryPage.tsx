import { Tabs, Typography } from 'antd';

import { MemoryCreateModal } from '../components/MemoryCreateModal';
import { MemoryEntitiesTab } from '../components/MemoryEntitiesTab';
import { MemorySearchTab } from '../components/MemorySearchTab';
import { MemoryStatsTab } from '../components/MemoryStatsTab';
import { MemorySummaryTab } from '../components/MemorySummaryTab';
import { useMemoryPage } from '../hooks/useMemoryPage';

const { Title } = Typography;

export const MemoryPage = () => {
  const {
    searchQuery,
    setSearchQuery,
    searchResults,
    loading,
    stats,
    entities,
    summary,
    sessionIdInput,
    setSessionIdInput,
    createOpen,
    setCreateOpen,
    newMemory,
    setNewMemory,
    handleSearch,
    handleAddMemory,
    handleDeleteMemory,
    loadEntities,
    loadSummary,
    resetNewMemory,
  } = useMemoryPage();

  return (
    <div style={{ padding: 24 }}>
      <Title level={3}>记忆管理</Title>
      <Tabs
        defaultActiveKey="search"
        items={[
          {
            key: 'search',
            label: '搜索',
            children: (
              <MemorySearchTab
                searchQuery={searchQuery}
                setSearchQuery={setSearchQuery}
                loading={loading}
                results={searchResults}
                onSearch={handleSearch}
                onCreate={() => setCreateOpen(true)}
                onDelete={handleDeleteMemory}
              />
            ),
          },
          {
            key: 'stats',
            label: '统计',
            children: <MemoryStatsTab stats={stats} />,
          },
          {
            key: 'entities',
            label: '实体图谱',
            children: <MemoryEntitiesTab entities={entities} onLoad={loadEntities} />,
          },
          {
            key: 'summary',
            label: '摘要',
            children: (
              <MemorySummaryTab
                sessionIdInput={sessionIdInput}
                setSessionIdInput={setSessionIdInput}
                summary={summary}
                onLoad={loadSummary}
              />
            ),
          },
        ]}
      />
      <MemoryCreateModal
        open={createOpen}
        value={newMemory}
        onChange={setNewMemory}
        onOk={handleAddMemory}
        onCancel={resetNewMemory}
      />
    </div>
  );
};

export default MemoryPage;
