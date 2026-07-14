import { PlusOutlined } from '@ant-design/icons';
import { Button, Col, Empty, Row } from 'antd';

import type { Workspace } from '../model/knowledge';

import { WorkspaceCard } from './WorkspaceCard';

interface WorkspaceListGridProps {
  workspaces: Workspace[];
  isAdmin: boolean;
  searchText: string;
  onOpen: (name: string) => void;
  onDelete: (name: string) => void;
  onCreate: () => void;
}

export const WorkspaceListGrid = ({
  workspaces,
  isAdmin,
  searchText,
  onOpen,
  onDelete,
  onCreate,
}: WorkspaceListGridProps) => {
  if (workspaces.length === 0) {
    return (
      <Empty
        description={searchText ? '没有找到匹配的知识库' : '还没有知识库'}
        style={{ padding: '60px 0' }}
      >
        {isAdmin && !searchText && (
          <Button type="primary" icon={<PlusOutlined />} onClick={onCreate}>
            创建第一个知识库
          </Button>
        )}
      </Empty>
    );
  }

  return (
    <Row className="responsive-card-grid" gutter={[16, 16]}>
      {workspaces.map((ws) => (
        <Col xs={24} sm={12} lg={8} key={ws.name}>
          <WorkspaceCard ws={ws} onDelete={onDelete} onOpen={onOpen} isAdmin={isAdmin} />
        </Col>
      ))}
    </Row>
  );
};
