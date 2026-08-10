import { Form, Select, message } from 'antd';
import { useCallback, useState } from 'react';
import type { z } from 'zod';

import { SCHEDULED_TASK_WORKFLOW_SELECT_SIZE } from '@/constants';
import { workflowApi } from '@/modules/workflow/api/workflow.api';
import {
  workflowSummarySchema,
  workflowVersionSummarySchema,
} from '@/modules/workflow/model/workflow';
import { extractErrorMessage } from '@/shared/lib';

type WorkflowSummary = z.infer<typeof workflowSummarySchema>;
type WorkflowVersionSummary = z.infer<typeof workflowVersionSummarySchema>;

/**
 * 工作流 + 版本级联懒加载 Select（ChatHeader 模式）。
 * 版本选项在选中工作流后才加载；切换工作流时清空已选版本。
 */
export function WorkflowVersionSelect() {
  const form = Form.useFormInstance();
  const workflowId = Form.useWatch('workflowId', form);

  const [workflows, setWorkflows] = useState<WorkflowSummary[]>([]);
  const [workflowLoading, setWorkflowLoading] = useState(false);
  const [versions, setVersions] = useState<WorkflowVersionSummary[]>([]);
  const [versionLoading, setVersionLoading] = useState(false);

  const loadWorkflows = useCallback(async () => {
    if (workflows.length) return;
    setWorkflowLoading(true);
    try {
      const result = await workflowApi.listWorkflows({ page: 1, pageSize: SCHEDULED_TASK_WORKFLOW_SELECT_SIZE });
      setWorkflows(result.workflows);
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '加载工作流列表失败'), duration: 0 });
    } finally {
      setWorkflowLoading(false);
    }
  }, [workflows.length]);

  const loadVersions = useCallback(async (selectedWorkflowId: string) => {
    setVersionLoading(true);
    try {
      const result = await workflowApi.listWorkflowVersions(selectedWorkflowId, {
        page: 1,
        pageSize: SCHEDULED_TASK_WORKFLOW_SELECT_SIZE,
      });
      setVersions(result.versions);
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '加载工作流版本失败'), duration: 0 });
    } finally {
      setVersionLoading(false);
    }
  }, []);

  const handleWorkflowChange = useCallback((nextWorkflowId: string) => {
    form.setFieldValue('versionId', undefined);
    setVersions([]);
    if (nextWorkflowId) {
      void loadVersions(nextWorkflowId);
    }
  }, [form, loadVersions]);

  return (
    <>
      <Form.Item
        label="工作流"
        name="workflowId"
        rules={[{ required: true, message: '请选择工作流' }]}
      >
        <Select
          aria-label="选择工作流"
          showSearch
          loading={workflowLoading}
          placeholder="搜索并选择工作流"
          optionFilterProp="label"
          onDropdownVisibleChange={(open) => {
            if (open) void loadWorkflows();
          }}
          onChange={handleWorkflowChange}
          options={workflows.map((w) => ({ value: w.id, label: w.name }))}
        />
      </Form.Item>
      <Form.Item
        label="版本"
        name="versionId"
        dependencies={['workflowId']}
        rules={[{ required: true, message: '请选择工作流版本' }]}
      >
        <Select
          aria-label="选择工作流版本"
          loading={versionLoading}
          placeholder={workflowId ? '选择已发布版本' : '请先选择工作流'}
          disabled={!workflowId}
          options={versions.map((v) => ({ value: v.id, label: `v${v.version} ${v.name}` }))}
        />
      </Form.Item>
    </>
  );
}
