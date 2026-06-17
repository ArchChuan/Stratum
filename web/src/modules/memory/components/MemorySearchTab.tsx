import { PlusOutlined, SearchOutlined } from '@ant-design/icons';
import { Button, Empty, Input, Space } from 'antd';

import type { MemorySearchResult } from '../model/memory';

import { MemoryEntryTable } from './MemoryEntryTable';

const { Search } = Input;

interface MemorySearchTabProps {
  searchQuery: string;
  setSearchQuery: (v: string) => void;
  loading: boolean;
  results: MemorySearchResult[];
  onSearch: (v: string) => void;
  onCreate: () => void;
  onDelete: (id: string) => void;
}

export const MemorySearchTab = ({
  searchQuery,
  setSearchQuery,
  loading,
  results,
  onSearch,
  onCreate,
  onDelete,
}: MemorySearchTabProps) => (
  <Space direction="vertical" style={{ width: '100%' }} size="middle">
    <Space>
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
      <Button icon={<PlusOutlined />} type="primary" onClick={onCreate}>
        添加记忆
      </Button>
    </Space>
    {results.length > 0 ? (
      <MemoryEntryTable results={results} onDelete={onDelete} />
    ) : (
      <Empty description="输入关键词搜索记忆" />
    )}
  </Space>
);
