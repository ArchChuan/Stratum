import React, { useState, useEffect } from 'react';
import { Card, Row, Col, Statistic, Button, Space, Table, Typography } from 'antd';
import { ArrowUpOutlined, ArrowDownOutlined, PlusOutlined } from '@ant-design/icons';
import { getAllSkills } from '../services/api';

const { Title } = Typography;

const DashboardPage = () => {
  const [stats, setStats] = useState({
    totalSkills: 0,
    codeSkills: 0,
    llmSkills: 0,
    otherSkills: 0
  });
  
  const [recentSkills, setRecentSkills] = useState([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    fetchData();
  }, []);

  const fetchData = async () => {
    try {
      setLoading(true);
      const response = await getAllSkills();
      const skills = response.data;
      
      // 计算统计数据
      const totalSkills = skills.length;
      const codeSkills = skills.filter(skill => skill.type === 'code').length;
      const llmSkills = skills.filter(skill => skill.type === 'llm').length;
      const otherSkills = skills.filter(skill => skill.type !== 'code' && skill.type !== 'llm').length;
      
      setStats({
        totalSkills,
        codeSkills,
        llmSkills,
        otherSkills
      });
      
      // 最近创建的技能（按时间排序取前5个）
      setRecentSkills(skills.slice(0, 5));
    } catch (error) {
      console.error('Error fetching dashboard data:', error);
    } finally {
      setLoading(false);
    }
  };

  const columns = [
    {
      title: '技能名称',
      dataIndex: 'name',
      key: 'name',
    },
    {
      title: '类型',
      dataIndex: 'type',
      key: 'type',
    },
    {
      title: '描述',
      dataIndex: 'description',
      key: 'description',
    },
    {
      title: '创建时间',
      dataIndex: 'createdAt',
      key: 'createdAt',
    },
  ];

  return (
    <div>
      <Row gutter={16}>
        <Col span={6}>
          <Card>
            <Statistic
              title="总技能数"
              value={stats.totalSkills}
              prefix={stats.totalSkills > 0 ? <ArrowUpOutlined /> : null}
            />
          </Card>
        </Col>
        <Col span={6}>
          <Card>
            <Statistic
              title="代码技能"
              value={stats.codeSkills}
              valueStyle={{ color: '#3f8600' }}
              prefix={stats.codeSkills > 0 ? <ArrowUpOutlined /> : null}
            />
          </Card>
        </Col>
        <Col span={6}>
          <Card>
            <Statistic
              title="LLM技能"
              value={stats.llmSkills}
              valueStyle={{ color: '#3f8600' }}
              prefix={stats.llmSkills > 0 ? <ArrowUpOutlined /> : null}
            />
          </Card>
        </Col>
        <Col span={6}>
          <Card>
            <Statistic
              title="其他技能"
              value={stats.otherSkills}
              prefix={stats.otherSkills > 0 ? <ArrowUpOutlined /> : null}
            />
          </Card>
        </Col>
      </Row>

      <div style={{ marginTop: 24 }}>
        <div style={{ marginBottom: 16, display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <Title level={3}>最近创建的技能</Title>
          <Button type="primary" icon={<PlusOutlined />} href="/skills/create">
            创建新技能
          </Button>
        </div>
        <Table 
          dataSource={recentSkills} 
          columns={columns} 
          rowKey="id" 
          loading={loading}
          pagination={{ pageSize: 5 }}
        />
      </div>
    </div>
  );
};

export default DashboardPage;