import { PlusOutlined, ReloadOutlined } from '@ant-design/icons';
import { Button, Card, Space, Table, Tag, Typography, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useCallback, useEffect, useState } from 'react';

import { mechanismApi } from '../api/mechanism.api';
import { ProfileEditDrawer } from '../components/ProfileEditDrawer';
import type { Profile } from '../model/mechanism';

import { extractErrorMessage } from '@/shared/lib';

const STATUS_COLORS: Record<string, string> = { active: 'green', draft: 'orange' };
const STATUS_LABELS: Record<string, string> = { active: '生效', draft: '建档' };

export const ModelProfilePage = () => {
  const [profiles, setProfiles] = useState<Profile[]>([]);
  const [loading, setLoading] = useState(true);
  const [editOpen, setEditOpen] = useState(false);
  const [editing, setEditing] = useState<Profile | null>(null);

  const reload = useCallback(async () => {
    try {
      setProfiles(await mechanismApi.list());
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '加载模型档案失败'), duration: 0 });
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void reload();
  }, [reload]);

  const openCreate = useCallback(() => {
    setEditing(null);
    setEditOpen(true);
  }, []);

  const openEdit = useCallback((p: Profile) => {
    setEditing(p);
    setEditOpen(true);
  }, []);

  const columns: ColumnsType<Profile> = [
    {
      title: '族键',
      dataIndex: 'family_key',
      key: 'family_key',
      render: (v: string, row) => (
        <Typography.Text strong onClick={() => openEdit(row)} style={{ cursor: 'pointer' }}>
          {v}
        </Typography.Text>
      ),
    },
    { title: '档案名称', dataIndex: 'display_name', key: 'display_name', render: (v: string) => v || '-' },
    {
      title: '家族前缀',
      dataIndex: 'family_prefixes',
      key: 'family_prefixes',
      render: (v: string[]) => v.map((p) => <Tag key={p}>{p}</Tag>),
    },
    {
      title: '状态',
      dataIndex: 'status',
      key: 'status',
      width: 90,
      render: (v?: string) =>
        v ? (
          <Tag color={STATUS_COLORS[v]}>{STATUS_LABELS[v] || v}</Tag>
        ) : (
          <Tag color="orange">未生效</Tag>
        ),
    },
    { title: '版本', dataIndex: 'version', key: 'version', width: 70, render: (v: number) => `v${v}` },
    {
      title: '更新时间',
      dataIndex: 'updated_at',
      key: 'updated_at',
      width: 170,
      render: (v: string) => (v ? new Date(v).toLocaleString() : '-'),
    },
  ];

  return (
    <div style={{ maxWidth: 1080, margin: '0 auto', padding: '24px 16px' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 16 }}>
        <div>
          <Typography.Title level={4} style={{ marginBottom: 4 }}>
            模型档案
          </Typography.Title>
          <Typography.Text type="secondary">
            机制基线按模型族建档（prompt 模板 / 管线模型 / 生效状态），平台管理面依附默认租户迭代；消费路径自动取用生效档案。
          </Typography.Text>
        </div>
        <Space>
          <Button icon={<ReloadOutlined />} onClick={() => void reload()} loading={loading}>
            刷新
          </Button>
          <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
            新建档案
          </Button>
        </Space>
      </div>

      <Card size="small" style={{ borderRadius: 12, border: '1px solid #f0f0f0' }}>
        <Table<Profile>
          rowKey="family_key"
          columns={columns}
          dataSource={profiles}
          loading={loading}
          pagination={false}
          locale={{ emptyText: '暂无模型档案，点击右上角新建' }}
          onRow={(row) => ({ onDoubleClick: () => openEdit(row) })}
        />
      </Card>

      <ProfileEditDrawer
        profile={editing}
        open={editOpen}
        onClose={() => setEditOpen(false)}
        onSaved={() => void reload()}
      />
    </div>
  );
};

export default ModelProfilePage;
