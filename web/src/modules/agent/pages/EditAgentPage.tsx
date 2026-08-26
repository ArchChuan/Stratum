import { ArrowLeftOutlined, LockOutlined } from '@ant-design/icons';
import { Button, Form, Skeleton, Typography, message } from 'antd';
import { useState } from 'react';

import { AgentFormSections } from '../components/AgentFormSections';
import { useEditAgentPage } from '../hooks/useEditAgentPage';

import { AGENT_DEFAULT_MAX_CONTEXT_TOKENS, AGENT_DEFAULT_MAX_ITERATIONS } from '@/constants';
import { useTenantRole } from '@/modules/iam';
import { operationProposalApi } from '@/modules/operation-gate';
import { extractErrorMessage } from '@/shared/lib';

const { Title, Text } = Typography;

export const EditAgentPage = () => {
  const {
    agent, form, loading, pageLoading, skills, mcpTools, workspaces, groupedModels,
    navigate, managementPath, onFinish, readOnly, editorCandidates, editorCandidatesLoading,
  } = useEditAgentPage();
  const { isAdmin } = useTenantRole();
  const [requesting, setRequesting] = useState(false);

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
    </div>
  );
};
