import { PlusOutlined, RobotOutlined } from '@ant-design/icons';
import { Button, Card, Col, Empty, Row, Skeleton } from 'antd';

import type { Agent } from '../model/agent';

import { AgentCard } from './AgentCard';

interface AgentsListGridProps {
  agents: Agent[];
  loading: boolean;
  searchText: string;
  onExecute: (a: Agent) => void;
  onEdit: (a: Agent) => void;
  onDelete: (id: string, name: string) => void;
  onCreate: () => void;
}

export const AgentsListGrid = ({
  agents,
  loading,
  searchText,
  onExecute,
  onEdit,
  onDelete,
  onCreate,
}: AgentsListGridProps) => {
  if (loading) {
    return (
      <Row gutter={[16, 16]}>
        {[1, 2, 3, 4, 5, 6].map((i) => (
          <Col xs={24} sm={12} lg={8} xl={6} key={i}>
            <Card
              style={{ borderRadius: 12, border: '1px solid #f0f0f0' }}
              styles={{ body: { padding: 20 } }}
            >
              <Skeleton active avatar paragraph={{ rows: 2 }} />
            </Card>
          </Col>
        ))}
      </Row>
    );
  }

  if (agents.length === 0) {
    return (
      <Empty
        image={<RobotOutlined style={{ fontSize: 48, color: '#d9d9d9' }} />}
        description={searchText ? '没有找到匹配的 Agent' : '还没有 Agent，点击右上角创建'}
        style={{ padding: '60px 0' }}
      >
        {!searchText && (
          <Button type="primary" icon={<PlusOutlined />} onClick={onCreate}>
            创建第一个 Agent
          </Button>
        )}
      </Empty>
    );
  }

  return (
    <Row gutter={[16, 16]}>
      {agents.map((agent) => (
        <Col xs={24} sm={12} lg={8} xl={6} key={agent.id}>
          <AgentCard agent={agent} onExecute={onExecute} onDelete={onDelete} onEdit={onEdit} />
        </Col>
      ))}
    </Row>
  );
};
