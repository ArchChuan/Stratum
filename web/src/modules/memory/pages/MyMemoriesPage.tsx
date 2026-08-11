import {
  ClearOutlined,
  DatabaseOutlined,
  TagsOutlined,
} from '@ant-design/icons';
import { Button, Card, Pagination, Space, Table, Tag, Tabs, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useCallback } from 'react';

import { useMyMemoriesPage } from '../hooks/useMyMemoriesPage';
import type { MemoryEntity, MemoryFact } from '../model/memory';

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
    clearLoading,
    total,
    page,
    pageSize,
    pageSizeOptions,
    entities,
    entitiesLoading,
    entityTotal,
    entityPage,
    entityPageSize,
    entityPageSizeOptions,
    handlePageChange,
    handleEntityPageChange,
    handleClearAll,
  } = useMyMemoriesPage();

  const factColumns: ColumnsType<MemoryFact> = [
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
  ];

  const entityColumns: ColumnsType<MemoryEntity> = [
    {
      title: '名称',
      dataIndex: 'name',
      ellipsis: true,
      render: (v: string) => <Typography.Text strong>{v}</Typography.Text>,
    },
    {
      title: '类型',
      dataIndex: 'entity_type',
      width: 120,
      render: (v: string) => <Tag color="blue">{v}</Tag>,
    },
    {
      title: '提及次数',
      dataIndex: 'fact_count',
      width: 100,
    },
    {
      title: '最近出现',
      dataIndex: 'last_seen_at',
      width: 160,
      render: (v: string) => new Date(v).toLocaleString(),
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
            description="将删除当前用户的所有记忆与实体，删除后不可恢复。"
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
          <Space size={16} wrap style={{ marginBottom: 16, width: '100%' }}>
            <StatCard
              loading={statsLoading}
              title="记忆条目"
              value={stats?.memory_count ?? 0}
              icon={<DatabaseOutlined />}
              color="#2563eb"
              bg="#dbeafe"
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

          <Tabs
            items={[
              {
                key: 'facts',
                label: '记忆条目',
                children: (
                  <>
                    <Table<MemoryFact>
                      rowKey="id"
                      columns={factColumns}
                      dataSource={memories}
                      loading={loading}
                      pagination={false}
                      locale={{
                        emptyText: (
                          <EmptyHint
                            title="记忆还是空的"
                            description="与 AI 助手的对话内容会被提取为记忆，展示在这里。"
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
                  </>
                ),
              },
              {
                key: 'entities',
                label: '记忆实体',
                children: (
                  <>
                    <Table<MemoryEntity>
                      rowKey="id"
                      columns={entityColumns}
                      dataSource={entities}
                      loading={entitiesLoading}
                      pagination={false}
                      locale={{
                        emptyText: (
                          <EmptyHint
                            title="还没有记忆实体"
                            description="对话中反复出现的话题会被识别为实体，展示在这里。"
                          />
                        ),
                      }}
                    />
                    <div style={{ display: 'flex', justifyContent: 'flex-end', marginTop: 16 }}>
                      <Pagination
                        current={entityPage}
                        pageSize={entityPageSize}
                        total={entityTotal}
                        showSizeChanger
                        pageSizeOptions={entityPageSizeOptions}
                        showTotal={(t) => `共 ${t} 条记录`}
                        onChange={handleEntityPageChange}
                      />
                    </div>
                  </>
                ),
              },
            ]}
          />
        </div>
      </Card>
    </div>
  );
};

export default MyMemoriesPage;
