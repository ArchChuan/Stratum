import { Modal, Select, Typography, message } from 'antd';
import { useEffect, useState } from 'react';

import { agentApi } from '../api/agent.api';

import { extractErrorMessage } from '@/shared/lib/errorMessage';

interface Props {
  open: boolean;
  onClose: () => void;
  onSaved: (llmModel: string) => void;
}

export const SystemAssistantModelModal = ({ open, onClose, onSaved }: Props) => {
  const [models, setModels] = useState<string[]>([]);
  const [selectedModel, setSelectedModel] = useState<string>();
  const [loading, setLoading] = useState(false);
  const [updateLoading, setUpdateLoading] = useState(false);

  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    setLoading(true);
    Promise.all([agentApi.models(), agentApi.getSystemSettings()])
      .then(([availableModels, settings]) => {
        if (cancelled) return;
        setModels(availableModels);
        setSelectedModel(settings.llmModel || undefined);
      })
      .catch((err) => {
        if (!cancelled) {
          message.error({ content: extractErrorMessage(err, '加载助手模型失败'), duration: 0 });
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [open]);

  const handleSave = async () => {
    if (!selectedModel) {
      message.error({ content: '请选择模型', duration: 0 });
      return;
    }
    setUpdateLoading(true);
    try {
      const settings = await agentApi.updateSystemSettings({ llmModel: selectedModel });
      onSaved(settings.llmModel);
      message.success({ content: '助手模型已更新', duration: 2 });
      onClose();
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '更新助手模型失败'), duration: 0 });
    } finally {
      setUpdateLoading(false);
    }
  };

  return (
    <Modal
      title="设置助手模型"
      open={open}
      onCancel={onClose}
      onOk={handleSave}
      okText="保存模型"
      cancelText="取消"
      confirmLoading={updateLoading}
      destroyOnHidden
    >
      <Typography.Paragraph type="secondary">
        平台负责助手的指令和工具边界，租户只需选择对话模型。
      </Typography.Paragraph>
      <Select
        aria-label="助手模型"
        value={selectedModel}
        onChange={setSelectedModel}
        loading={loading}
        placeholder="请选择模型"
        options={models.map((model) => ({ value: model, label: model }))}
        style={{ width: '100%' }}
        showSearch
        optionFilterProp="label"
      />
    </Modal>
  );
};
