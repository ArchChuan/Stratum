import { Button, Descriptions, Drawer, Empty, Input, List, message, Modal, Space, Spin, Tag } from 'antd';
import { useEffect, useState } from 'react';

import { useSnapshotsTab } from '../hooks/useSnapshotsTab';
import type { MemorySnapshot } from '../model/memory';

import { MEMORY_SNAPSHOT_ITEM_MAX_RUNES, MEMORY_SNAPSHOT_SECTION_MAX_ITEMS } from '@/constants';
import { DangerPopconfirm } from '@/shared/ui';

interface EditableListProps {
  header: string;
  items: string[];
  onChange: (items: string[]) => void;
}

// EditableList 行内可编辑列表：行内 Input 添加（回车提交），校验条数/单条长度
// 上限（对齐后端 ActiveSnapshotSectionMaxItems/ItemMaxRunes）。
const EditableList = ({ header, items, onChange }: EditableListProps) => {
  const [draft, setDraft] = useState('');

  const submit = () => {
    const value = draft.trim();
    if (!value) return;
    if (value.length > MEMORY_SNAPSHOT_ITEM_MAX_RUNES) {
      message.warning({ content: `单条最多 ${MEMORY_SNAPSHOT_ITEM_MAX_RUNES} 个字符`, duration: 2 });
      return;
    }
    if (items.length >= MEMORY_SNAPSHOT_SECTION_MAX_ITEMS) {
      message.warning({ content: `每段最多 ${MEMORY_SNAPSHOT_SECTION_MAX_ITEMS} 条`, duration: 2 });
      return;
    }
    onChange([...items, value]);
    setDraft('');
  };

  return (
    <List
      header={header}
      dataSource={items}
      renderItem={(item, i) => (
        <List.Item
          actions={[
            <Button key="rm" type="link" size="small" danger onClick={() => onChange(items.filter((_, j) => j !== i))}>
              删除
            </Button>,
          ]}
        >
          <List.Item.Meta description={item} />
        </List.Item>
      )}
      footer={
        <Space.Compact style={{ width: '100%' }}>
          <Input
            placeholder="输入后回车添加"
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onPressEnter={submit}
            maxLength={MEMORY_SNAPSHOT_ITEM_MAX_RUNES}
            allowClear
          />
          <Button onClick={submit}>添加</Button>
        </Space.Compact>
      }
    />
  );
};

// 只读三段落展示（Drawer 详情用），空段显示占位。
const ReadonlySections = ({ snapshot }: { snapshot: MemorySnapshot }) => (
  <Space direction="vertical" style={{ width: '100%' }}>
    <List size="small" header="工作上下文" dataSource={snapshot.work_context} locale={{ emptyText: <Empty /> }} renderItem={(item) => <List.Item>{item}</List.Item>} />
    <List size="small" header="个人上下文" dataSource={snapshot.personal_context} locale={{ emptyText: <Empty /> }} renderItem={(item) => <List.Item>{item}</List.Item>} />
    <List size="small" header="当前关注" dataSource={snapshot.top_of_mind} locale={{ emptyText: <Empty /> }} renderItem={(item) => <List.Item>{item}</List.Item>} />
  </Space>
);

// 标题：agent_name · conversation_name；conversation 缺失回落 agent_name，
// 再缺失回落 agent_id（#24 后端 COALESCE 保证空串，前端兜底 agent_id）。
const snapshotTitle = (s: MemorySnapshot): string => {
  const name = s.agent_name || s.agent_id;
  return s.conversation_name ? `${name} · ${s.conversation_name}` : name;
};

const isExpired = (s: MemorySnapshot): boolean => new Date(s.expires_at).getTime() < Date.now();

export const SnapshotPanel = ({ onChanged, reloadKey }: { onChanged?: () => void; reloadKey?: number }) => {
  const { snapshots, loading, saveLoading, deleteLoading, updateSnapshot, deleteSnapshot, reload } = useSnapshotsTab();
  const [detail, setDetail] = useState<MemorySnapshot | null>(null);
  const [editing, setEditing] = useState<MemorySnapshot | null>(null);
  const [editOpen, setEditOpen] = useState(false);
  const [workContext, setWorkContext] = useState<string[]>([]);
  const [personalContext, setPersonalContext] = useState<string[]>([]);
  const [topOfMind, setTopOfMind] = useState<string[]>([]);

  useEffect(() => {
    if (reloadKey && reloadKey > 0) void reload();
    // eslint-disable-next-line react-hooks/exhaustive-deps -- 仅响应清空后的 reloadKey 变化
  }, [reloadKey]);

  const openEdit = (s: MemorySnapshot) => {
    setEditing(s);
    setWorkContext(s.work_context);
    setPersonalContext(s.personal_context);
    setTopOfMind(s.top_of_mind);
    setEditOpen(true);
  };

  const handleSave = async () => {
    if (!editing) return;
    try {
      await updateSnapshot(editing.agent_id, {
        work_context: workContext,
        personal_context: personalContext,
        top_of_mind: topOfMind,
      });
      setEditOpen(false);
      onChanged?.();
    } catch {
      // updateSnapshot 已弹错误 toast；保持 Modal 打开，编辑草稿保留（避免 unhandled rejection）。
    }
  };

  const handleDelete = async (agentId: string) => {
    await deleteSnapshot(agentId);
    setDetail(null);
    onChanged?.();
  };

  return (
    <Spin spinning={loading}>
      {snapshots.length === 0 ? (
        <Empty description="活跃快照还是空的" />
      ) : (
        <List
          dataSource={snapshots}
          renderItem={(s) => {
            const expired = isExpired(s);
            return (
              <List.Item
                style={{ opacity: expired ? 0.55 : 1 }}
                actions={[
                  <Button key="detail" type="link" size="small" onClick={() => setDetail(s)}>
                    查看详情
                  </Button>,
                  <Button key="edit" type="link" size="small" onClick={() => openEdit(s)}>
                    编辑
                  </Button>,
                  <DangerPopconfirm
                    key="clear"
                    title="清空快照"
                    description="清空后该 Agent 的活跃上下文将重置，且无法恢复"
                    onConfirm={() => void handleDelete(s.agent_id)}
                  >
                    <Button type="link" size="small" danger loading={deleteLoading}>
                      清空
                    </Button>
                  </DangerPopconfirm>,
                ]}
              >
                <List.Item.Meta
                  title={
                    <Space>
                      {snapshotTitle(s)}
                      {expired && <Tag color="default">已过期</Tag>}
                    </Space>
                  }
                  description={`更新时间：${new Date(s.updated_at).toLocaleString()} · Agent ${s.agent_id}`}
                />
              </List.Item>
            );
          }}
        />
      )}

      <Drawer open={detail !== null} onClose={() => setDetail(null)} title={detail ? snapshotTitle(detail) : ''} width={480} destroyOnHidden>
        {detail && (
          <Space direction="vertical" style={{ width: '100%' }} size="large">
            <ReadonlySections snapshot={detail} />
            <Descriptions column={1} bordered size="small">
              <Descriptions.Item label="Agent">{detail.agent_id}</Descriptions.Item>
              <Descriptions.Item label="状态">{detail.status}</Descriptions.Item>
              <Descriptions.Item label="过期时间">{new Date(detail.expires_at).toLocaleString()}</Descriptions.Item>
              <Descriptions.Item label="更新时间">{new Date(detail.updated_at).toLocaleString()}</Descriptions.Item>
            </Descriptions>
          </Space>
        )}
      </Drawer>

      <Modal
        title="编辑快照"
        open={editOpen}
        onCancel={() => setEditOpen(false)}
        onOk={() => void handleSave()}
        confirmLoading={saveLoading}
        destroyOnHidden
        width={640}
      >
        {editing && (
          <Space direction="vertical" style={{ width: '100%' }}>
            <EditableList header="工作上下文" items={workContext} onChange={setWorkContext} />
            <EditableList header="个人上下文" items={personalContext} onChange={setPersonalContext} />
            <EditableList header="当前关注" items={topOfMind} onChange={setTopOfMind} />
          </Space>
        )}
      </Modal>
    </Spin>
  );
};
