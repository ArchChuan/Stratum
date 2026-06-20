import { SearchOutlined } from '@ant-design/icons';
import { Empty, Input, Space } from 'antd';

import type { MemorySearchResult } from '../model/memory';

import { MemoryEntryTable } from './MemoryEntryTable';

const { Search } = Input;

interface MemorySearchTabProps {
  searchQuery: string;
  setSearchQuery: (v: string) => void;
  loading: boolean;
  results: MemorySearchResult[];
  onSearch: (v: string) => void;
  onDelete: (id: string) => void;
}

export const MemorySearchTab = ({
  searchQuery,
  setSearchQuery,
  loading,
  results,
  onSearch,
  onDelete,
}: MemorySearchTabProps) => (
  <Space direction="vertical" style={{ width: '100%' }} size="middle">
    <Search
      placeholder="搜索记忆..."
      value={searchQuery}
      onChange={(e) => setSearchQuery(e.target.value)}
      onSearch={onSearch}
      enterButton={
        <>
          <SearchOutlined /> 搜索
        </>
      }
      style={{ width: 400 }}
      loading={loading}
      allowClear
    />
    {results.length > 0 ? (
      <MemoryEntryTable results={results} onDelete={onDelete} />
    ) : (
      <Empty description="输入关键词搜索记忆" />
    )}
  </Space>
);
