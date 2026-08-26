import { ArrowLeftOutlined, LockOutlined } from '@ant-design/icons';
import { Button, Form, Skeleton, Tabs, Typography, message } from 'antd';
import { useEffect, useState } from 'react';

import { agentApi } from '../api/agent.api';
import { AgentFormSections } from '../components/AgentFormSections';
import { useEditAgentPage } from '../hooks/useEditAgentPage';
import type { AgentVersion } from '../model/agent';

import { AGENT_DEFAULT_MAX_CONTEXT_TOKENS, AGENT_DEFAULT_MAX_ITERATIONS } from '@/constants';
import { useTenantRole } from '@/modules/iam';
import { operationProposalApi } from '@/modules/operation-gate';
import { extractErrorMessage } from '@/shared/lib';
import { VersionHistory, type VersionRow } from '@/shared/ui';

const { Title, Text } = Typography;

export const EditAgentPage = () => {
  const {
    agent, form, loading, pageLoading, skills, mcpTools, workspaces, groupedModels,
    navigate, managementPath, onFinish, readOnly, refreshTick, reloadAgent,
    editorCandidates, editorCandidatesLoading,
  } = useEditAgentPage();
  const { isAdmin } = useTenantRole();
  const [requesting, setRequesting] = useState(false);
  const [activeTab, setActiveTab] = useState('config');
  const [versions, setVersions] = useState<AgentVersion[]>([]);
  const [versionsLoading, setVersionsLoading] = useState(false);

  // 版本历史：进入该 tab 才加载；保存/回滚后 bump refreshTick 触发重拉。
  // 依赖用原始 agentId 而非整个 agent 对象，避免 reloadAgent 换新对象导致重复拉取。
  const agentId = agent?.id;
  useEffect(() => {
    if (activeTab !== 'versions' || !agentId) return;
    let cancelled = false;
    setVersionsLoading(true);
    agentApi.listVersions(agentId).then((rows) => { if (!cancelled) setVersions(rows ?? []); })
      .catch((err) => { if (!cancelled) message.error({ content: extractErrorMessage(err, '加载版本历史失败'), duration: 3 }); })
      .finally(() => { if (!cancelled) setVersionsLoading(false); });
    return () => { cancelled = true; };
  }, [activeTab, agentId, refreshTick]);

  // 回滚会改变当前 agent 配置：原地重拉 agent 回填表单并重载版本历史。
  const handleRollback = async (row: VersionRow) => {
    if (!agent) return;
    await agentApi.rollback(agent.id, row.id);
    await reloadAgent();
  };

  // P3：普通成员（非白名单）申请编辑权限 → grant_editor 提案，管理员审批后即授予。
  const handleRequestEditor = async () => {
    if (!agent) return;
    setRequesting(true);
    try {
      await operationProposalApi.requestEditorAccess('agent', agent.id, { resourceName: agent.name });
      message.success({ content: '已提交，等待管理员审批', duration: 3 });
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '提交申请失败'), duration: 3 });
    } finally {
      setRequesting(false);
    }
  };

  if (pageLoading) {
    return (
      <div className="responsive-form-page">
        <Skeleton active paragraph={{ rows: 1 }} style={{ marginBottom: 24 }} />
        <div
          style={{
            background: '#fff',
            borderRadius: 12,
            border: '1px solid #f0f0f0',
            padding: 24,
            marginBottom: 16,
          }}
        >
          <Skeleton active paragraph={{ rows: 3 }} />
        </div>
        <div
          style={{
            background: '#fff',
            borderRadius: 12,
            border: '1px solid #f0f0f0',
            padding: 24,
          }}
        >
          <Skeleton active paragraph={{ rows: 4 }} />
        </div>
      </div>
    );
  }

  return (
    <div className="responsive-form-page">
      <div className="responsive-detail-header" style={{ marginBottom: 24 }}>
        <Button icon={<ArrowLeftOutlined />} onClick={() => navigate(managementPath)} type="text">
          返回
        </Button>
        <div>
          <Title level={4} style={{ margin: 0 }}>
            {readOnly ? '查看 Agent 配置' : '编辑 Agent'}
          </Title>
          <Text type="secondary" style={{ fontSize: 13 }}>
            {readOnly ? '只读查看，如需修改请申请编辑权限' : '修改 Agent 配置'}
          </Text>
        </div>
      </div>

      <Tabs activeKey={activeTab} onChange={setActiveTab} items={[
        { key: 'config', label: '配置', children: (
          <>
            <Form
              form={form}
              layout="vertical"
              onFinish={onFinish}
              disabled={readOnly}
              initialValues={{
                maxIterations: AGENT_DEFAULT_MAX_ITERATIONS,
                maxContextTokens: AGENT_DEFAULT_MAX_CONTEXT_TOKENS,
                allowedSkills: [],
                memoryScope: 'user',
              }}
            >
              <AgentFormSections
                skills={skills}
                mcpTools={mcpTools}
                workspaces={workspaces}
                groupedModels={groupedModels}
                currentModel={agent?.llmModel}
                // P2：可编辑人（白名单）管理仅 admin/owner 可见；readOnly 时表单 disabled。
                showEditors={isAdmin}
                editorCandidates={editorCandidates}
                editorCandidatesLoading={editorCandidatesLoading}
              />

              {!readOnly && (
                <div className="responsive-form-actions" style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
                  <Button onClick={() => navigate(managementPath)}>取消</Button>
                  <Button type="primary" htmlType="submit" loading={loading}>
                    保存修改
                  </Button>
                </div>
              )}
            </Form>
            {/* 申请编辑权限按钮必须放在 Form 外：<Form disabled={readOnly}> 通过 DisabledContext
                禁用表单内所有 antd 组件（含 Button），member 只读时须可点申请。 */}
            {readOnly && (
              <div className="responsive-form-actions" style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
                <Button type="primary" icon={<LockOutlined />} loading={requesting} onClick={() => void handleRequestEditor()}>
                  申请编辑权限
                </Button>
              </div>
            )}
          </>
        ) },
        { key: 'versions', label: '版本历史', children: (
          <VersionHistory
            rows={(versions ?? []).map((v) => ({
              id: v.id, versionNo: v.versionNo, status: v.status, isCurrent: v.isCurrent,
              createdByName: v.createdByName, createdBy: v.createdBy, createdAt: v.createdAt,
              canRollback: v.status === 'deprecated' && !readOnly,
            }))}
            loading={versionsLoading}
            rollback={handleRollback}
            infoMessage="保存即产生新版本并立即生效；历史版本可回滚，回滚不产生新版本。"
          />
        ) },
      ]} />
    </div>
  );
};
