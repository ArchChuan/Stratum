import { PlusOutlined, SearchOutlined } from '@ant-design/icons';
import { Button, Input, Space } from 'antd';

interface AgentsListFiltersProps {
  searchText: string;
  onSearchChange: (v: string) => void;
  onCreate: () => void;
  /** 仅管理员可见「创建 Agent」入口，普通成员隐藏。 */
  canManage?: boolean;
}

export const AgentsListFilters = ({
  searchText,
  onSearchChange,
  onCreate,
  canManage = false,
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
    {canManage && (
      <Button type="primary" icon={<PlusOutlined />} onClick={onCreate}>
        创建 Agent
      </Button>
    )}
  </Space>
);
