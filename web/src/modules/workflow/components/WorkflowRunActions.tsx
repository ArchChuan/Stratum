import { Button, Modal, Space, message } from 'antd';

import { workflowApi } from '../api/workflow.api';
import type { WorkflowRunState } from '../model/run-state';

interface RequestError { response?: { data?: { error?: string }; status?: number } }

export const WorkflowRunActions = ({ state, onChanged }: { state: WorkflowRunState; onChanged: () => void }) => {
  const execute = (action: 'cancel' | 'pause' | 'resume') => Modal.confirm({
    title: action === 'cancel' ? '取消这次运行？' : action === 'pause' ? '暂停这次运行？' : '继续这次运行？',
    content: action === 'cancel' ? '取消后无法恢复，已产生的外部副作用不会自动撤销。' : '操作将基于当前运行 generation 执行。',
    okText: '确认', cancelText: '返回',
    onOk: async () => {
      try {
        if (action === 'cancel') await workflowApi.cancelWorkflowRun(state.run.id, { expected_generation: state.run.generation, reason: '用户取消' });
        if (action === 'pause') await workflowApi.pauseWorkflowRun(state.run.id, { expected_generation: state.run.generation, reason: '管理员暂停' });
        if (action === 'resume') await workflowApi.resumeWorkflowRun(state.run.id, { expected_generation: state.run.generation });
        message.success({ content: '操作成功', duration: 2 });
        onChanged();
      } catch (error: unknown) {
        const requestError = error as RequestError;
        message.error({ content: requestError.response?.status === 409 ? '运行状态已变化，已重新加载最新状态' : requestError.response?.data?.error || '操作失败', duration: 0 });
        onChanged();
      }
    },
  });
  return <Space wrap>
    {state.availableActions.includes('pause') && <Button onClick={() => execute('pause')}>暂停</Button>}
    {state.availableActions.includes('resume') && <Button onClick={() => execute('resume')}>继续</Button>}
    {state.availableActions.includes('cancel') && <Button danger onClick={() => execute('cancel')}>取消运行</Button>}
  </Space>;
};
