import { Tabs, Typography } from 'antd';

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
    summary,
    sessionIdInput,
    setSessionIdInput,
    handleSearch,
    handleDeleteMemory,
    loadSummary,
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
    </div>
  );
};

export default MemoryPage;
