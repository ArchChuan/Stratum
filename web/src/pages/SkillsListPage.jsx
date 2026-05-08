import React, { useState, useEffect } from 'react';
import { 
  Table, 
  Button, 
  Space, 
  Tag, 
  Modal, 
  Card, 
  Typography, 
  Input,
  notification,
  Alert
} from 'antd';
import { PlusOutlined, DeleteOutlined, InfoCircleOutlined } from '@ant-design/icons';
import { getAllSkills } from '../services/api';
import { Link } from 'react-router-dom';

const { Search } = Input;
const { Title } = Typography;

const SkillsListPage = () => {
  const [skills, setSkills] = useState([]);
  const [loading, setLoading] = useState(true);
  const [searchText, setSearchText] = useState('');

  useEffect(() => {
    fetchSkills();
  }, []);

  const fetchSkills = async () => {
    try {
      setLoading(true);
      const response = await getAllSkills();
      setSkills(response.data);
    } catch (error) {
      console.error('Error fetching skills:', error);
      notification.error({
        message: '获取技能列表失败',
        description: error.message,
      });
    } finally {
      setLoading(false);
    }
  };

  const handleDeleteSkill = (skillId) => {
    // TODO: 实现删除技能功能
    console.log('Delete skill:', skillId);
  };

  // 过滤技能列表
  const filteredSkills = skills.filter(skill =>
    skill.name.toLowerCase().includes(searchText.toLowerCase()) ||
    skill.description.toLowerCase().includes(searchText.toLowerCase())
  );

  const columns = [
    {
      title: 'ID',
      dataIndex: 'id',
      key: 'id',
      width: 200,
      ellipsis: true,
    },
    {
      title: '技能名称',
      dataIndex: 'name',
      key: 'name',
      sorter: (a, b) => a.name.localeCompare(b.name),
    },
    {
      title: '类型',
      dataIndex: 'type',
      key: 'type',
      render: (type) => {
        let color = 'blue';
        if (type === 'code') color = 'green';
        else if (type === 'llm') color = 'orange';
        else color = 'default';
        
        return <Tag color={color}>{type}</Tag>;
      },
    },
    {
      title: '描述',
      dataIndex: 'description',
      key: 'description',
      ellipsis: true,
    },
    {
      title: '创建时间',
      dataIndex: 'createdAt',
      key: 'createdAt',
      render: (date) => new Date(date).toLocaleString(),  // 修复了这里，添加了箭头函数
    },
    {
      title: '操作',
      key: 'actions',
      render: (_, record) => (
        <Space size="middle">
          <Button 
            type="link" 
            disabled
            title="技能只能通过代理执行"
          >
            <InfoCircleOutlined /> 仅可通过代理执行
          </Button>
          <Button 
            type="link" 
            danger 
            icon={<DeleteOutlined />}
            onClick={() => handleDeleteSkill(record.id)}
          >
            删除
          </Button>
        </Space>
      ),
    },
  ];

  return (
    <div>
      <div style={{ marginBottom: 16, display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <Title level={2}>技能管理</Title>
        <Space>
          <Search
            placeholder="搜索技能名称或描述"
            value={searchText}
            onChange={(e) => setSearchText(e.target.value)}
            style={{ width: 300 }}
          />
          <Button type="primary" icon={<PlusOutlined />} href="/skills/create">
            创建技能
          </Button>
        </Space>
      </div>

      <Alert
        message="重要提醒"
        description={
          <div>
            <p>技能只能通过智能代理执行，不能直接调用。要使用技能，请前往<Link to="/agents/create">创建智能代理</Link>或<Link to="/agents">管理现有代理</Link>。</p>
          </div>
        }
        type="info"
        showIcon
        style={{ marginBottom: 16 }}
      />

      <Card>
        <Table 
          dataSource={filteredSkills} 
          columns={columns} 
          rowKey="id" 
          loading={loading}
          pagination={{ 
            pageSize: 10,
            showSizeChanger: true,
            showQuickJumper: true,
            showTotal: (total) => `总计 ${total} 条`,
          }}
        />
      </Card>
    </div>
  );
};

export default SkillsListPage;