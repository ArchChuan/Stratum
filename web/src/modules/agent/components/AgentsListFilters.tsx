import { PlusOutlined, SearchOutlined } from '@ant-design/icons';
import { Button, Input, Space } from 'antd';

interface AgentsListFiltersProps {
  searchText: string;
  onSearchChange: (v: string) => void;
  onCreate: () => void;
}

export const AgentsListFilters = ({
  searchText,
  onSearchChange,
  onCreate,
}: AgentsListFiltersProps) => (
  <Space className="responsive-toolbar" size={8}>
    <Input
      placeholder="搜索 Agent..."
      prefix={<SearchOutlined style={{ color: '#bfbfbf' }} />}
      value={searchText}
      onChange={(e) => onSearchChange(e.target.value)}
      allowClear
      style={{ width: '100%', maxWidth: 220 }}
    />
    <Button type="primary" icon={<PlusOutlined />} onClick={onCreate}>
      创建 Agent
    </Button>
  </Space>
);
