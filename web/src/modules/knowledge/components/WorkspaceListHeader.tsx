import { PlusOutlined, SearchOutlined } from '@ant-design/icons';
import { Button, Input, Space, Typography } from 'antd';

const { Title, Text } = Typography;

interface WorkspaceListHeaderProps {
  searchText: string;
  isAdmin: boolean;
  onSearchChange: (v: string) => void;
  onCreate: () => void;
}

export const WorkspaceListHeader = ({
  searchText,
  isAdmin,
  onSearchChange,
  onCreate,
}: WorkspaceListHeaderProps) => (
  <div
    style={{
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'space-between',
      marginBottom: 20,
    }}
  >
    <div>
      <Title level={4} style={{ margin: 0 }}>
        知识库
      </Title>
      <Text type="secondary" style={{ fontSize: 13 }}>
        管理向量知识空间
      </Text>
    </div>
    <Space size={8}>
      <Input
        placeholder="搜索知识库..."
        prefix={<SearchOutlined style={{ color: '#bfbfbf' }} />}
        value={searchText}
        onChange={(e) => onSearchChange(e.target.value)}
        allowClear
        style={{ width: 200 }}
      />
      {isAdmin && (
        <Button type="primary" icon={<PlusOutlined />} onClick={onCreate}>
          新建知识库
        </Button>
      )}
    </Space>
  </div>
);
