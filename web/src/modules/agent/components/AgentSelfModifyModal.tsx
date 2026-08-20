import { Form, Input, InputNumber, Modal, message } from 'antd';
import { useCallback, useState } from 'react';

import type { Agent, AgentFormValues } from '../model/agent';

import {
  AGENT_DEFAULT_MAX_CONTEXT_TOKENS,
  AGENT_DEFAULT_MAX_ITERATIONS,
  AGENT_MAX_CONTEXT_TOKENS_MAX,
  AGENT_MAX_CONTEXT_TOKENS_MIN,
  AGENT_MAX_CONTEXT_TOKENS_STEP,
} from '@/constants';
import { operationProposalApi } from '@/modules/operation-gate';
import { extractErrorMessage } from '@/shared/lib';

interface AgentSelfModifyModalProps {
  agent: Agent | null;
  open: boolean;
  onClose: () => void;
}

/**
 * 成员发起「自修改」：内容变更不直通，一律生成操作提案等待审批。
 * 提交全量字段（关联选择项从原 Agent 值带回，本次仅允许修改核心字段），
 * 202 时提示待审批与提案号，不跳转。
 */
export const AgentSelfModifyModal = ({ agent, open, onClose }: AgentSelfModifyModalProps) => {
  const [form] = Form.useForm<AgentFormValues>();
  const [loading, setLoading] = useState(false);

  const handleSubmit = useCallback(async () => {
    if (!agent) return;
    let values: AgentFormValues;
    try {
      values = await form.validateFields();
    } catch {
      return; // 校验失败：表单内已红字提示，不产生未捕获 rejection
    }
    setLoading(true);
    try {
      const result = await operationProposalApi.selfModify(agent.id, {
        ...values,
        // 关联选择项本次不可修改，原样带回保持全量语义（服务端整体覆盖）。
        allowedSkills: agent.allowedSkills || [],
        mcpToolIds: agent.mcpToolIds || [],
        knowledgeWorkspaceIds: agent.knowledgeWorkspaceIds || [],
      });
      if (result.status === 'pending_approval') {
        message.info({ content: `已提交修改审批，提案号：${result.proposalId}`, duration: 4 });
      } else {
        message.success({ content: '修改已通过审批并生效', duration: 2 });
        if (result.usageWarning) {
          message.warning({ content: result.usageWarning, duration: 3 });
        }
      }
      onClose();
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '提交自修改失败'), duration: 3 });
    } finally {
      setLoading(false);
    }
  }, [agent, form, onClose]);

  return (
    <Modal
      title={`发起自修改 · ${agent?.name || ''}`}
      open={open}
      onCancel={onClose}
      onOk={() => void handleSubmit()}
      confirmLoading={loading}
      okText="提交审批"
      cancelText="取消"
      width={520}
      destroyOnClose
    >
      {/* destroyOnClose + initialValues：预填随 Form mount 生效，与 store 时序无关
          （rc-dialog children 延迟渲染会清掉 setFieldsValue 的写入） */}
      <Form
        form={form}
        layout="vertical"
        preserve={false}
        initialValues={
          agent
            ? {
                name: agent.name,
                description: agent.description,
                systemPrompt: agent.systemPrompt,
                llmModel: agent.llmModel,
                maxIterations: agent.maxIterations ?? AGENT_DEFAULT_MAX_ITERATIONS,
                maxContextTokens: agent.maxContextTokens ?? AGENT_DEFAULT_MAX_CONTEXT_TOKENS,
                memoryScope: agent.memoryScope || 'user',
              }
            : undefined
        }
      >
        <Form.Item name="name" label="名称" rules={[{ required: true, message: '请输入名称' }]}>
          <Input maxLength={100} />
        </Form.Item>
        <Form.Item name="description" label="描述">
          <Input.TextArea rows={2} maxLength={500} />
        </Form.Item>
        <Form.Item name="systemPrompt" label="系统提示词">
          <Input.TextArea rows={4} maxLength={20000} />
        </Form.Item>
        <Form.Item name="llmModel" label="模型">
          <Input placeholder="如 qwen-plus" />
        </Form.Item>
        <Form.Item name="maxIterations" label="最大迭代次数">
          <InputNumber min={1} max={100} style={{ width: '100%' }} />
        </Form.Item>
        <Form.Item
          name="maxContextTokens"
          label="最大上下文 Tokens"
          extra="0 = 自动按模型窗口解析（该弹窗模型为手动输入，不随窗口联动）"
        >
          <InputNumber min={AGENT_MAX_CONTEXT_TOKENS_MIN} max={AGENT_MAX_CONTEXT_TOKENS_MAX} step={AGENT_MAX_CONTEXT_TOKENS_STEP} style={{ width: '100%' }} />
        </Form.Item>
        <Form.Item name="memoryScope" label="记忆范围">
          <Input />
        </Form.Item>
      </Form>
    </Modal>
  );
};
