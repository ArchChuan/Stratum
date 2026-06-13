import React, { useState, useEffect } from 'react';
import {
  Button, Tag, Input, Modal, message, Typography, Card,
  Row, Col, Tooltip, Popconfirm, Empty, Skeleton, Space,
} from 'antd';
import {
  PlusOutlined, DeleteOutlined, SearchOutlined, ThunderboltOutlined,
  CodeOutlined, RobotOutlined, PartitionOutlined, GlobalOutlined,
} from '@ant-design/icons';
import { getAllSkills, deleteSkill } from '../services/api';
import { useNavigate } from 'react-router-dom';

const { Title, Text, Paragraph } = Typography;

const TYPE_META = {
  code: { color: '#52c41a', bg: '#f6ffed', label: 'Code', icon: <CodeOutlined /> },
  llm: { color: '#1677ff', bg: '#e6f4ff', label: 'LLM', icon: <RobotOutlined /> },
  http: { color: '#fa8c16', bg: '#fff7e6', label: 'HTTP', icon: <GlobalOutlined /> },
  default: { color: '#8c8c8c', bg: '#f5f5f5', label: '其他', icon: <PartitionOutlined /> },
};
const typeMeta = (t) => TYPE_META[t] || TYPE_META.default;

const SkillCard = ({ skill, onDelete }) => {
  const meta = typeMeta(skill.type);
  return (
    <Card
      style={{ borderRadius: 12, border: '1px solid #f0f0f0', height: '100%', display: 'flex', flexDirection: 'column' }}
      styles={{ body: { padding: 20, flex: 1, display: 'flex', flexDirection: 'column' } }}
      hoverable
    >
      <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', marginBottom: 12 }}>
        <div style={{
          width: 40, height: 40, borderRadius: 10,
          background: meta.bg,
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          flexShrink: 0, fontSize: 18, color: meta.color,
        }}>
          {meta.icon}
        </div>
        <div style={{ display: 'flex', gap: 4, alignItems: 'center' }}>
          <Tag style={{ border: 'none', borderRadius: 6, fontSize: 11, background: meta.bg, color: meta.color, fontWeight: 500 }}>
            {meta.label}
          </Tag>
          {skill.type === 'code' && skill.config?.language && (
            <Tag style={{ border: 'none', borderRadius: 6, fontSize: 10, background: '#f5f5f5', color: '#666', margin: 0 }}>
              {skill.config.language}
            </Tag>
          )}
        </div>
      </div>

      <Text strong style={{ fontSize: 15, marginBottom: 4, display: 'block' }}>{skill.name}</Text>
      <Paragraph
        type="secondary"
        ellipsis={{ rows: 2 }}
        style={{ fontSize: 13, marginBottom: 12, flex: 1, marginTop: 0 }}
      >
        {skill.description || '暂无描述'}
      </Paragraph>

      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', paddingTop: 12, borderTop: '1px solid #f5f5f5' }}>
        <Text type="secondary" style={{ fontSize: 12 }}>
          {skill.created_at ? new Date(skill.created_at).toLocaleDateString('zh-CN') : '-'}
        </Text>
        <Tooltip title="通过 Agent 执行此技能">
          <Popconfirm
            title={`确定删除技能 "${skill.name}" 吗？`}
            onConfirm={() => onDelete(skill.id)}
            okText="删除" okType="danger" cancelText="取消"
          >
            <Button type="text" size="small" danger icon={<DeleteOutlined />} />
          </Popconfirm>
        </Tooltip>
      </div>
    </Card>
  );
};

const SkillsListPage = () => {
  const navigate = useNavigate();
  const [skills, setSkills] = useState([]);
  const [loading, setLoading] = useState(true);
  const [searchText, setSearchText] = useState('');

  useEffect(() => {
    let cancelled = false;
    (async () => {
      setLoading(true);
      try {
        const res = await getAllSkills();
        if (!cancelled) setSkills(res.data.skills || []);
      } catch {
        if (!cancelled) message.error('获取技能列表失败');
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => { cancelled = true; };
  }, []);

  const handleDelete = async (skillId) => {
    try {
      await deleteSkill(skillId);
      message.success('技能已删除');
      setSkills(prev => prev.filter(s => s.id !== skillId));
    } catch (err) {
      message.error(err.response?.data?.error || '删除失败');
    }
  };

  const filteredSkills = skills.filter(s =>
    s.name.toLowerCase().includes(searchText.toLowerCase()) ||
    (s.description || '').toLowerCase().includes(searchText.toLowerCase())
  );

  return (
    <div>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 20 }}>
        <div>
          <Title level={4} style={{ margin: 0 }}>技能列表</Title>
          <Text type="secondary" style={{ fontSize: 13 }}>技能通过 Agent 调用执行</Text>
        </div>
        <Space size={8}>
          <Input
            placeholder="搜索技能..."
            prefix={<SearchOutlined style={{ color: '#bfbfbf' }} />}
            value={searchText}
            onChange={(e) => setSearchText(e.target.value)}
            allowClear
            style={{ width: 220 }}
          />
          <Button type="primary" icon={<PlusOutlined />} onClick={() => navigate('/skills/create')}>
            创建技能
          </Button>
        </Space>
      </div>

      {loading ? (
        <Row gutter={[16, 16]}>
          {[1, 2, 3, 4].map(i => (
            <Col xs={24} sm={12} lg={8} xl={6} key={i}>
              <Card style={{ borderRadius: 12, border: '1px solid #f0f0f0' }} styles={{ body: { padding: 20 } }}>
                <Skeleton active avatar paragraph={{ rows: 2 }} />
              </Card>
            </Col>
          ))}
        </Row>
      ) : filteredSkills.length === 0 ? (
        <Empty
          image={<ThunderboltOutlined style={{ fontSize: 48, color: '#d9d9d9' }} />}
          description={searchText ? '没有找到匹配的技能' : '还没有技能，点击右上角创建'}
          style={{ padding: '60px 0' }}
        >
          {!searchText && (
            <Button type="primary" icon={<PlusOutlined />} onClick={() => navigate('/skills/create')}>
              创建第一个技能
            </Button>
          )}
        </Empty>
      ) : (
        <Row gutter={[16, 16]}>
          {filteredSkills.map(skill => (
            <Col xs={24} sm={12} lg={8} xl={6} key={skill.id}>
              <SkillCard skill={skill} onDelete={handleDelete} />
            </Col>
          ))}
        </Row>
      )}
    </div>
  );
};

export default SkillsListPage;
