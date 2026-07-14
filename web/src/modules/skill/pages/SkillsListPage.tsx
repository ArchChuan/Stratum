import {
  PlusOutlined,
  SearchOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import { Button, Card, Col, Empty, Input, Row, Skeleton, Space, Typography } from 'antd';

import { SkillCard } from '../components/SkillCard';
import { useSkillsListPage } from '../hooks/useSkillsListPage';

import { useTenantRole } from '@/modules/iam';

const { Title, Text } = Typography;

export const SkillsListPage = () => {
  const { skills, loading, searchText, setSearchText, navigate, handleDelete } =
    useSkillsListPage();
  const { isAdmin } = useTenantRole();

  return (
    <div>
      <div
        className="responsive-page-header"
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          marginBottom: 20,
        }}
      >
        <div>
          <Title level={4} style={{ margin: 0 }}>
            技能列表
          </Title>
          <Text type="secondary" style={{ fontSize: 13 }}>
            技能通过 Agent 调用执行
          </Text>
        </div>
        <Space className="responsive-toolbar" size={8}>
          <Input
            placeholder="搜索技能..."
            prefix={<SearchOutlined style={{ color: '#bfbfbf' }} />}
            value={searchText}
            onChange={(e) => setSearchText(e.target.value)}
            allowClear
            style={{ width: '100%', maxWidth: 220 }}
          />
          {isAdmin && (
            <Button
              type="primary"
              icon={<PlusOutlined />}
              onClick={() => navigate('/skills/create')}
            >
              创建技能
            </Button>
          )}
        </Space>
      </div>

      {loading ? (
        <Row className="responsive-card-grid" gutter={[16, 16]}>
          {[1, 2, 3, 4].map((i) => (
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
      ) : skills.length === 0 ? (
        <Empty
          image={<ThunderboltOutlined style={{ fontSize: 48, color: '#d9d9d9' }} />}
          description={
            searchText ? '没有找到匹配的技能' : isAdmin ? '还没有技能，点击右上角创建' : '还没有技能'
          }
          style={{ padding: '60px 0' }}
        >
          {!searchText && isAdmin && (
            <Button
              type="primary"
              icon={<PlusOutlined />}
              onClick={() => navigate('/skills/create')}
            >
              创建第一个技能
            </Button>
          )}
        </Empty>
      ) : (
        <Row className="responsive-card-grid" gutter={[16, 16]}>
          {skills.map((skill) => (
            <Col xs={24} sm={12} lg={8} xl={6} key={skill.id}>
              <SkillCard
                skill={skill}
                onEdit={(id) => navigate(`/skills/${id}/workspace`)}
                onDelete={handleDelete}
                canManage={isAdmin}
              />
            </Col>
          ))}
        </Row>
      )}
    </div>
  );
};

export default SkillsListPage;
