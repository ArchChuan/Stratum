import { ClearOutlined, DatabaseOutlined, TagsOutlined } from '@ant-design/icons';
import { Alert, Button, Card, Space, Tabs } from 'antd';
import { Link } from 'react-router-dom';

import { EntityTable } from '../components/EntityTable';
import { EntryTable } from '../components/EntryTable';
import { FactTable } from '../components/FactTable';
import { SnapshotPanel } from '../components/SnapshotPanel';
import { SummaryTable } from '../components/SummaryTable';
import { useMyMemoriesPage } from '../hooks/useMyMemoriesPage';

import { StatCard } from '@/modules/dashboard';
import { DangerPopconfirm } from '@/shared/ui';

export const MyMemoriesPage = () => {
  const { stats, statsLoading, clearLoading, handleClearAll, reloadKey, reloadStats } = useMyMemoriesPage();

  return (
    <div>
      <Card
        title="我的记忆"
        styles={{ body: { padding: 0 } }}
        extra={
          <DangerPopconfirm
            title="清空全部记忆"
            description="将删除该用户的全部事实、实体、摘要、快照与原始条目，并同步清理向量，无法恢复"
            onConfirm={() => void handleClearAll()}
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
                  记忆可能无法写入，请到 <Link to="/tenant/settings">租户配置页</Link> 配置嵌入模型。
                </span>
              }
            />
          )}
          <Space size={16} wrap style={{ marginBottom: 16, width: '100%' }}>
            <StatCard
              loading={statsLoading}
              title="事实记忆"
              value={stats?.memory_count ?? 0}
              icon={<DatabaseOutlined />}
              color="#2563eb"
              bg="#dbeafe"
            />
            <StatCard
              loading={statsLoading}
              title="话题实体"
              value={stats?.entity_count ?? 0}
              icon={<TagsOutlined />}
              color="#eb2f96"
              bg="#fff0f6"
            />
          </Space>

          <Tabs
            defaultActiveKey="facts"
            items={[
              { key: 'facts', label: '事实', children: <FactTable onChanged={reloadStats} reloadKey={reloadKey} /> },
              { key: 'entities', label: '实体', children: <EntityTable onChanged={reloadStats} reloadKey={reloadKey} /> },
              { key: 'summaries', label: '摘要', children: <SummaryTable onChanged={reloadStats} reloadKey={reloadKey} /> },
              { key: 'snapshots', label: '快照', children: <SnapshotPanel onChanged={reloadStats} reloadKey={reloadKey} /> },
              { key: 'entries', label: '条目', children: <EntryTable onChanged={reloadStats} reloadKey={reloadKey} /> },
            ]}
          />
        </div>
      </Card>
    </div>
  );
};

export default MyMemoriesPage;
