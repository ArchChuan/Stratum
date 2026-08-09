import { Button, Form, Input, Modal, type FormInstance } from 'antd';
import { useEffect } from 'react';

import type { ScheduledTask } from '../model/scheduledTask';

import { WorkflowVersionSelect } from './WorkflowVersionSelect';

import { SCHEDULED_TASK_MAX_NAME_LENGTH } from '@/constants';

export interface ScheduledTaskFormValues {
  name: string;
  workflowId: string;
  versionId: string;
  /** 工作流输入模板，TextArea 中以 JSON 字符串编辑，提交时解析。 */
  inputTemplate: string;
  cronExpr: string;
}

interface ScheduledTaskFormModalProps {
  open: boolean;
  loading: boolean;
  form: FormInstance<ScheduledTaskFormValues>;
  editing?: ScheduledTask | null;
  onClose: () => void;
  onSubmit: (values: ScheduledTaskFormValues) => void;
}

const parseJsonObject = (raw: string): Record<string, unknown> => {
  if (!raw.trim()) return {};
  const parsed = JSON.parse(raw);
  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
    throw new Error('输入模板必须是 JSON 对象');
  }
  return parsed;
};

/** 5 段 cron 或 @ 描述符（@daily 等）。精确校验由后端承担，此处拦截明显非法的输入。 */
const cronPattern = /^(@(?:annually|yearly|monthly|weekly|daily|hourly|midnight))|^(@every\s+\S+)|^(\S+\s+\S+\s+\S+\s+\S+\s+\S+)$/;

export function ScheduledTaskFormModal({
  open,
  loading,
  form,
  editing,
  onClose,
  onSubmit,
}: ScheduledTaskFormModalProps) {
  useEffect(() => {
    if (open && editing) {
      form.setFieldsValue({
        name: editing.name,
        workflowId: editing.workflowId,
        versionId: editing.versionId,
        inputTemplate: JSON.stringify(editing.inputTemplate, null, 2),
        cronExpr: editing.cronExpr,
      });
    } else if (open) {
      form.setFieldsValue({ inputTemplate: '{\n  "task": ""\n}' });
    }
  }, [open, editing, form]);

  const handleClose = () => {
    onClose();
    form.resetFields();
  };

  return (
    <Modal
      title={editing ? '编辑定时任务' : '新建定时任务'}
      open={open}
      onCancel={handleClose}
      destroyOnHidden
      width={560}
      styles={{ body: { maxHeight: 'min(70vh, 640px)', overflowY: 'auto' } }}
      footer={null}
    >
      <Form form={form} layout="vertical" onFinish={onSubmit}>
        <Form.Item
          label="名称"
          name="name"
          rules={[
            { required: true, message: '请输入任务名称' },
            { max: SCHEDULED_TASK_MAX_NAME_LENGTH, message: `名称最多 ${SCHEDULED_TASK_MAX_NAME_LENGTH} 个字符` },
          ]}
        >
          <Input placeholder="例：每日数据汇总" maxLength={SCHEDULED_TASK_MAX_NAME_LENGTH} />
        </Form.Item>
        <WorkflowVersionSelect />
        <Form.Item
          label="Cron 表达式"
          name="cronExpr"
          rules={[
            { required: true, message: '请输入 cron 表达式' },
            {
              pattern: cronPattern,
              message: '格式：5 段 cron（分 时 日 月 周）或 @daily 等描述符',
            },
          ]}
          extra="按 UTC 时区触发，最小间隔 60 秒。例：0 9 * * 1 表示每周一 09:00"
        >
          <Input placeholder="0 9 * * 1" />
        </Form.Item>
        <Form.Item
          label="输入模板"
          name="inputTemplate"
          rules={[
            { required: true, message: '请输入输入模板' },
            {
              validator: (_, value: string) => {
                try {
                  parseJsonObject(value);
                  return Promise.resolve();
                } catch (err) {
                  return Promise.reject(new Error(err instanceof Error ? err.message : 'JSON 解析失败'));
                }
              },
            },
          ]}
          extra="将作为工作流输入，勿存放密钥；必填字段以所选工作流的输入 schema 为准"
        >
          <Input.TextArea rows={6} placeholder={'{\n  "task": ""\n}'} />
        </Form.Item>
        <Form.Item style={{ marginBottom: 0 }}>
          <Button type="primary" htmlType="submit" block loading={loading}>
            {editing ? '保存' : '创建'}
          </Button>
        </Form.Item>
      </Form>
    </Modal>
  );
}
