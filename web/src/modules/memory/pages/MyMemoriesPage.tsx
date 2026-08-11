import {
  BookOutlined,
  ClearOutlined,
  DatabaseOutlined,
  TagsOutlined,
} from '@ant-design/icons';
import { Alert, Button, Card, Pagination, Space, Table, Tag, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useCallback } from 'react';
import { Link } from 'react-router-dom';

import { useMyMemoriesPage } from '../hooks/useMyMemoriesPage';
import type { MemoryFact } from '../model/memory';

import { StatCard } from '@/modules/dashboard';
import { DangerPopconfirm, EmptyHint } from '@/shared/ui';

const IMPORTANCE_COLORS: Record<string, string> = { high: 'red', medium: 'orange', low: 'green' };

const importanceTag = (importance: number): { color: string; label: string } => {
  if (importance >= 0.7) return { color: IMPORTANCE_COLORS.high, label: '重要' };
  if (importance >= 0.4) return { color: IMPORTANCE_COLORS.medium, label: '一般' };
  return { color: IMPORTANCE_COLORS.low, label: '次要' };
};

export const MyMemoriesPage = () => {
  const {
    memories,
    loading,
    stats,
    statsLoading,
    deleteLoading,
    clearLoading,
    total,
    page,
    pageSize,
    pageSizeOptions,
    handlePageChange,
    handleDelete,
    handleClearAll,
  } = useMyMemoriesPage();

  const columns: ColumnsType<MemoryFact> = [
    {
      title: '内容',
      dataIndex: 'content',
      ellipsis: true,
      render: (v: string) => <Typography.Text style={{ wordBreak: 'break-all' }}>{v}</Typography.Text>,
    },
    {
      title: '重要度',
      dataIndex: 'importance',
      width: 90,
      render: (v: number) => {
        const tag = importanceTag(v);
        return <Tag color={tag.color}>{tag.label}</Tag>;
      },
    },
    {
      title: '创建时间',
      dataIndex: 'created_at',
      width: 160,
      render: (v: string) => new Date(v).toLocaleString(),
    },
    {
      title: '操作',
      key: 'actions',
      width: 80,
      render: (_, record) => (
        <DangerPopconfirm
          title="删除这条记忆？"
          description="删除后不可恢复，该记忆将从记忆中移除。"
          onConfirm={() => handleDelete(record.id)}
          loading={deleteLoading === record.id}
        />
      ),
    },
  ];

  const handleClear = useCallback(() => {
    void handleClearAll();
  }, [handleClearAll]);

  return (
    <div>
      <Card
        title="我的记忆"
        styles={{ body: { padding: 0 } }}
        extra={
          <DangerPopconfirm
            title="清空全部记忆？"
            description="将删除当前用户的所有记忆，删除后不可恢复。"
            onConfirm={handleClear}
            loading={clearLoading}
          >
            <Button danger icon={<ClearOutlined />} loading={clearLoading}>
              清空全部
            </Button>
          </DangerPopconfirm>
        }
      >
        <div style={{ padding: 16 }}>
          {stats && stats.embed_model_configured === false && (
            <Alert
              type="warning"
              showIcon
              style={{ marginBottom: 16 }}
              message="未配置嵌入模型"
              description={
                <span>
                  记忆可能无法写入，请到{' '}
                  <Link to="/models">模型管理页</Link> 配置嵌入模型。
                </span>
              }
            />
          )}
          <Space size={16} wrap style={{ marginBottom: 16, width: '100%' }}>
            <StatCard
              loading={statsLoading}
              title="记忆条目"
              value={stats?.total_entries ?? 0}
              icon={<DatabaseOutlined />}
              color="#2563eb"
              bg="#dbeafe"
            />
            <StatCard
              loading={statsLoading}
              title="长期记忆"
              value={stats?.long_term_count ?? 0}
              icon={<BookOutlined />}
              color="#722ed1"
              bg="#f9f0ff"
            />
            <StatCard
              loading={statsLoading}
              title="短期记忆"
              value={stats?.short_term_count ?? 0}
              icon={<BookOutlined />}
              color="#13c2c2"
              bg="#e6fffb"
            />
            <StatCard
              loading={statsLoading}
              title="记忆实体"
              value={stats?.entity_count ?? 0}
              icon={<TagsOutlined />}
              color="#eb2f96"
              bg="#fff0f6"
            />
          </Space>

          <Table<MemoryFact>
            rowKey="id"
            columns={columns}
            dataSource={memories}
            loading={loading}
            pagination={false}
            locale={{
              emptyText: (
                <EmptyHint
                  title="记忆还是空的"
                  description="与 AI 助手的对话内容会被提取为长期记忆，展示在这里。"
                />
              ),
            }}
          />

          <div style={{ display: 'flex', justifyContent: 'flex-end', marginTop: 16 }}>
            <Pagination
              current={page}
              pageSize={pageSize}
              total={total}
              showSizeChanger
              pageSizeOptions={pageSizeOptions}
              showTotal={(t) => `共 ${t} 条记录`}
              onChange={handlePageChange}
            />
          </div>
        </div>
      </Card>
    </div>
  );
};

export default MyMemoriesPage;
