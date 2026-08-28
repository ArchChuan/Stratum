import { SearchOutlined } from '@ant-design/icons';
import {
  Button,
  Descriptions,
  Drawer,
  Form,
  Input,
  InputNumber,
  Modal,
  Pagination,
  Select,
  Space,
  Table,
  Tag,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useEffect, useState } from 'react';

import { useFactsTab } from '../hooks/useFactsTab';
import type { MemoryFact } from '../model/memory';

import { MemoryScopeTag } from './MemoryScopeTag';

import { FACT_CATEGORIES } from '@/constants';
import { DangerPopconfirm, EmptyHint } from '@/shared/ui';

const columns = (onEdit: (f: MemoryFact) => void, onDelete: (id: string) => void, deleteLoading: boolean): ColumnsType<MemoryFact> => [
  { title: '内容', dataIndex: 'content', ellipsis: true },
  { title: '归属', dataIndex: 'scope', width: 100, render: (v: string) => <MemoryScopeTag scope={v} /> },
  { title: '分类', dataIndex: 'category', width: 100, render: (v: string) => <Tag>{v}</Tag> },
  { title: '重要度', dataIndex: 'importance', width: 90, render: (v: number) => v.toFixed(2) },
  { title: '来源', dataIndex: 'source', width: 130, ellipsis: true },
  { title: '更新时间', dataIndex: 'updated_at', width: 170, render: (v: string) => new Date(v).toLocaleString() },
  {
    title: '操作',
    key: 'action',
    width: 140,
    render: (_: unknown, record: MemoryFact) => (
      <Space>
        <Button type="link" size="small" onClick={() => onEdit(record)}>
          编辑
        </Button>
        <DangerPopconfirm
          title="删除事实"
          description="删除后该事实将不再进入记忆上下文，且无法恢复"
          onConfirm={() => onDelete(record.id)}
        >
          <Button type="link" size="small" danger loading={deleteLoading}>
            删除
          </Button>
        </DangerPopconfirm>
      </Space>
    ),
  },
];

export const FactTable = ({ onChanged, reloadKey }: { onChanged?: () => void; reloadKey?: number }) => {
  const { facts, loading, deleteLoading, filters, applyFilters, updateFact, deleteFact, pagination, reload } = useFactsTab();
  const [detail, setDetail] = useState<MemoryFact | null>(null);
  const [editOpen, setEditOpen] = useState(false);
  const [editing, setEditing] = useState<MemoryFact | null>(null);
  const [form] = Form.useForm();
  const [saveLoading, setSaveLoading] = useState(false);
  // 搜索框本地草稿：Enter/blur 才提交，避免逐键请求；外部筛选状态（清空/重载）变化时同步。
  const [qDraft, setQDraft] = useState(filters.q);

  useEffect(() => {
    setQDraft(filters.q);
  }, [filters.q]);

  useEffect(() => {
    if (reloadKey && reloadKey > 0) void reload();
    // eslint-disable-next-line react-hooks/exhaustive-deps -- 仅响应清空后的 reloadKey 变化；reload 内部已随筛选/翻页状态由 hook 自身 effect 触发
  }, [reloadKey]);

  const handleDelete = async (id: string) => {
    await deleteFact(id);
    onChanged?.();
  };

  const openEdit = (f: MemoryFact) => {
    setEditing(f);
    setDetail(null);
    form.setFieldsValue({ content: f.content, importance: f.importance, category: f.category });
    setEditOpen(true);
  };

  const handleSave = async () => {
    const values = await form.validateFields();
    if (!editing) return;
    setSaveLoading(true);
    try {
      await updateFact(editing.id, values);
      setEditOpen(false);
      onChanged?.();
    } finally {
      setSaveLoading(false);
    }
  };

  return (
    <div>
      <Space style={{ marginBottom: 16 }} wrap>
        <Input
          placeholder="搜索事实内容"
          prefix={<SearchOutlined />}
          allowClear
          value={qDraft}
          onChange={(e) => setQDraft(e.target.value)}
          onPressEnter={() => applyFilters({ ...filters, q: qDraft })}
          onBlur={() => applyFilters({ ...filters, q: qDraft })}
          style={{ width: 220 }}
        />
        <Select
          placeholder="分类"
          allowClear
          style={{ width: 130 }}
          options={FACT_CATEGORIES.map((c) => ({ label: c, value: c }))}
          onChange={(v?: string) => applyFilters({ ...filters, category: v })}
        />
        <InputNumber
          placeholder="重要度 ≥"
          min={0}
          max={1}
          step={0.1}
          style={{ width: 110 }}
          onChange={(v: number | null) => applyFilters({ ...filters, importanceMin: v ?? undefined })}
        />
      </Space>
      <Table<MemoryFact>
        rowKey="id"
        columns={columns((f) => openEdit(f), (id) => void handleDelete(id), deleteLoading)}
        dataSource={facts}
        loading={loading}
        onRow={(record) => ({ onClick: () => setDetail(record) })}
        pagination={false}
        locale={{ emptyText: <EmptyHint title={facts.length === 0 ? '事实记忆还是空的' : '没有找到匹配的事实'} /> }}
      />
      <Pagination {...pagination} />

      <Drawer open={detail !== null} onClose={() => setDetail(null)} title="事实详情" width={480} destroyOnHidden>
        {detail && (
          <Descriptions column={1} bordered size="small">
            <Descriptions.Item label="内容">{detail.content}</Descriptions.Item>
            <Descriptions.Item label="归属">
              <MemoryScopeTag scope={detail.scope} />
            </Descriptions.Item>
            <Descriptions.Item label="分类">{detail.category}</Descriptions.Item>
            <Descriptions.Item label="重要度">{detail.importance}</Descriptions.Item>
            <Descriptions.Item label="置信度">{detail.confidence}</Descriptions.Item>
            <Descriptions.Item label="来源">{detail.source}</Descriptions.Item>
            <Descriptions.Item label="创建时间">{new Date(detail.created_at).toLocaleString()}</Descriptions.Item>
            <Descriptions.Item label="更新时间">{new Date(detail.updated_at).toLocaleString()}</Descriptions.Item>
          </Descriptions>
        )}
      </Drawer>

      <Modal
        title="编辑事实"
        open={editOpen}
        onCancel={() => setEditOpen(false)}
        onOk={handleSave}
        confirmLoading={saveLoading}
        destroyOnHidden
      >
        <Form form={form} layout="vertical" style={{ marginTop: 16 }}>
          <Form.Item name="content" label="内容" rules={[{ required: true, message: '请输入内容' }]}>
            <Input.TextArea rows={4} maxLength={1000} showCount />
          </Form.Item>
          <Form.Item name="importance" label="重要度（0-1）" rules={[{ required: true, message: '请输入重要度' }]}>
            <InputNumber min={0} max={1} step={0.1} style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item name="category" label="分类" rules={[{ required: true, message: '请选择分类' }]}>
            <Select options={FACT_CATEGORIES.map((c) => ({ label: c, value: c }))} />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
};
