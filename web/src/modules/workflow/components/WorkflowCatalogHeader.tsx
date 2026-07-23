import { BranchesOutlined, PlusOutlined, SearchOutlined } from '@ant-design/icons';
import { Button, Input, Space, Typography } from 'antd';

const { Paragraph, Title } = Typography;

export const WorkflowCatalogHeader = ({
  query,
  canManage,
  onSearch,
  onCreate,
}: {
  query: string;
  canManage: boolean;
  onSearch: (value: string) => void;
  onCreate: () => void;
}) => (
  <header className="workflow-catalog-header">
    <div className="workflow-catalog-heading">
      <span className="workflow-section-mark"><BranchesOutlined /></span>
      <div>
        <Title level={3}>工作流</Title>
        <Paragraph>把稳定的业务步骤发布成可重复执行、可追踪的流程。</Paragraph>
      </div>
    </div>
    <Space wrap className="workflow-catalog-actions">
      <Input
        allowClear
        prefix={<SearchOutlined />}
        placeholder="搜索工作流"
        value={query}
        onChange={(event) => onSearch(event.target.value)}
      />
      {canManage && <Button aria-label="新建工作流" type="primary" icon={<PlusOutlined />} onClick={onCreate}>新建工作流</Button>}
    </Space>
  </header>
);
