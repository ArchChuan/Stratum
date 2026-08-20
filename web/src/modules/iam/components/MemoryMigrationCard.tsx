import { DatabaseOutlined } from '@ant-design/icons';
import { Alert, Button, Modal, Progress, Select, Space, Tag, Typography } from 'antd';
import { useState } from 'react';

import { useMemoryMigration } from '../hooks/useMemoryMigration';

import { ModelHealthBadge } from '@/modules/llm/components/ModelHealthBadge';
import { SectionHeader } from '@/shared/ui';

const { Text } = Typography;
const { Option, OptGroup } = Select;

const STATUS_META: Record<string, { color: string; label: string }> = {
  migrating: { color: 'processing', label: '迁移中' },
  done: { color: 'success', label: '已完成' },
  failed: { color: 'error', label: '失败' },
  canceled: { color: 'warning', label: '已取消' },
};

// 不可用模型禁用：与后端解析链 isModelUsable 语义一致（unhealthy/halfOpen
// fail-closed 不选中），防止把熔断模型配成迁移目标。
const isUnusable = (health?: string) => health === 'unhealthy' || health === 'half_open';

// 租户配置页的记忆嵌入模型平滑迁移卡片（P5 确认制切换）：展示当前生效模型与
// 最近迁移状态，管理员选目标模型 → 确认成本 → 启动迁移 → 进度/取消/重试。
export const MemoryMigrationCard = () => {
  const {
    migration,
    loading,
    currentModel,
    models,
    modelsLoading,
    targetModel,
    setTargetModel,
    starting,
    canceling,
    retrying,
    fetchCost,
    startMigration,
    cancelMigration,
    retryMigration,
  } = useMemoryMigration();

  const [confirming, setConfirming] = useState(false);

  const handleStart = async () => {
    if (!targetModel || confirming) return;
    setConfirming(true);
    const cost = await fetchCost();
    setConfirming(false);
    if (!cost) return;
    Modal.confirm({
      className: 'mobile-overlay',
      title: '确认切换嵌入模型？',
      content: (
        <div>
          <p>将当前生效模型切换到「{targetModel}」，切换立即生效。</p>
          <p>
            迁移成本：共 {cost.fact_count} 条已提取事实，预计约 {cost.estimated_seconds} 秒完成后台回填。
          </p>
          <p>迁移完成前读取由旧模型兜底，不中断业务；迁移期间新数据直接写入新模型。</p>
        </div>
      ),
      okText: '确认迁移',
      cancelText: '取消',
      onOk: () => startMigration(targetModel),
    });
  };

  const statusMeta = migration ? STATUS_META[migration.status] : undefined;
  const progressPercent =
    migration && migration.total_facts > 0
      ? Math.round((migration.progress / migration.total_facts) * 100)
      : 0;

  return (
    <div
      style={{
        background: '#fff',
        borderRadius: 12,
        border: '1px solid #f0f0f0',
        padding: 24,
      }}
    >
      <SectionHeader
        icon={<DatabaseOutlined />}
        title="记忆嵌入模型"
        subtitle="确认制切换：切换立即生效，存量事实后台渐进重嵌入，迁移完成前读取由旧模型兜底，不中断业务"
      />

      <Space direction="vertical" size={16} style={{ width: '100%' }}>
        <div>
          <Text type="secondary" style={{ display: 'block', marginBottom: 4 }}>
            当前生效模型
          </Text>
          {currentModel ? (
            <Tag color="blue">{currentModel}</Tag>
          ) : (
            <Text type="secondary">未配置（请选择目标模型并开始迁移）</Text>
          )}
        </div>

        <Space align="center" wrap>
          <Select
            value={targetModel}
            onChange={setTargetModel}
            placeholder="选择目标嵌入模型"
            showSearch
            optionFilterProp="children"
            loading={modelsLoading}
            allowClear
            style={{ width: 320 }}
            disabled={migration?.status === 'migrating'}
          >
            {models.map((group) => (
              <OptGroup key={group.provider} label={group.provider}>
                {group.models.map((m) => (
                  <Option key={m.value} value={m.value} disabled={isUnusable(m.health)}>
                    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
                      {m.label}
                      <ModelHealthBadge health={m.health} />
                    </span>
                  </Option>
                ))}
              </OptGroup>
            ))}
          </Select>
          <Button
            type="primary"
            onClick={handleStart}
            loading={starting || confirming}
            disabled={
              !targetModel || targetModel === currentModel || migration?.status === 'migrating'
            }
          >
            开始迁移
          </Button>
        </Space>

        {loading && <Text type="secondary">加载中…</Text>}

        {migration && (
          <div style={{ background: '#fafafa', borderRadius: 8, padding: 16 }}>
            <Space direction="vertical" size={8} style={{ width: '100%' }}>
              <Space wrap>
                <Text strong>
                  {migration.from_model} → {migration.to_model}
                </Text>
                {statusMeta && <Tag color={statusMeta.color}>{statusMeta.label}</Tag>}
              </Space>

              {migration.status === 'migrating' && (
                <>
                  <Progress
                    percent={progressPercent}
                    status="active"
                    format={() => `${migration.progress} / ${migration.total_facts}`}
                  />
                  <Text type="secondary">
                    正在后台回填已提取事实到目标模型，迁移完成前读取由旧模型兜底。
                  </Text>
                  <Button danger onClick={() => cancelMigration(migration.id)} loading={canceling}>
                    取消迁移
                  </Button>
                </>
              )}

              {migration.status === 'done' && (
                <Text type="secondary">
                  迁移已完成，旧模型集合保留一段时间，可随时发起反向迁移回滚。
                </Text>
              )}

              {migration.status === 'failed' && (
                <Alert
                  type="error"
                  showIcon
                  message="迁移失败"
                  description="可重试从断点继续回填，或选择其他目标模型重新发起迁移。"
                  action={
                    <Button size="small" onClick={() => retryMigration(migration.id)} loading={retrying}>
                      重试
                    </Button>
                  }
                />
              )}

              {migration.status === 'canceled' && (
                <Alert
                  type="warning"
                  showIcon
                  message="迁移已取消"
                  description="可重试从断点继续回填，或选择其他目标模型重新发起迁移。"
                  action={
                    <Button size="small" onClick={() => retryMigration(migration.id)} loading={retrying}>
                      重试
                    </Button>
                  }
                />
              )}
            </Space>
          </div>
        )}
      </Space>
    </div>
  );
};

export default MemoryMigrationCard;
