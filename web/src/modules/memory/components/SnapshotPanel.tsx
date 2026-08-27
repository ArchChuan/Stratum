import { Button, Card, Col, Empty, Input, List, message, Modal, Row, Space, Spin } from 'antd';
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

export const SnapshotPanel = ({ onChanged, reloadKey }: { onChanged?: () => void; reloadKey?: number }) => {
  const { snapshots, loading, saveLoading, deleteLoading, updateSnapshot, deleteSnapshot, reload } = useSnapshotsTab();
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
    await updateSnapshot(editing.agent_id, {
      work_context: workContext,
      personal_context: personalContext,
      top_of_mind: topOfMind,
    });
    setEditOpen(false);
    onChanged?.();
  };

  const handleDelete = async (agentId: string) => {
    await deleteSnapshot(agentId);
    onChanged?.();
  };

  return (
    <Spin spinning={loading}>
      {snapshots.length === 0 ? (
        <Empty description="活跃快照还是空的" />
      ) : (
        <Row gutter={[16, 16]}>
          {snapshots.map((s) => (
            <Col key={s.agent_id} xs={24} md={12} xl={8}>
              <Card
                title={`Agent ${s.agent_id}`}
                extra={
                  <Space>
                    <Button size="small" onClick={() => openEdit(s)}>
                      编辑
                    </Button>
                    <DangerPopconfirm
                      title="清空快照"
                      description="清空后该 Agent 的活跃上下文将重置，且无法恢复"
                      onConfirm={() => void handleDelete(s.agent_id)}
                    >
                      <Button size="small" danger loading={deleteLoading}>
                        清空
                      </Button>
                    </DangerPopconfirm>
                  </Space>
                }
              >
                <List size="small" header="工作上下文" dataSource={s.work_context} renderItem={(item) => <List.Item>{item}</List.Item>} locale={{ emptyText: <Empty /> }} />
                <List size="small" header="个人上下文" dataSource={s.personal_context} renderItem={(item) => <List.Item>{item}</List.Item>} locale={{ emptyText: <Empty /> }} />
                <List size="small" header="当前关注" dataSource={s.top_of_mind} renderItem={(item) => <List.Item>{item}</List.Item>} locale={{ emptyText: <Empty /> }} />
              </Card>
            </Col>
          ))}
        </Row>
      )}

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
