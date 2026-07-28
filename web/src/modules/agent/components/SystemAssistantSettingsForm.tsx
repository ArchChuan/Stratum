import { Alert, Button, Form, Select, Typography, message } from 'antd';
import { useEffect, useRef, useState } from 'react';

import { agentApi } from '../api/agent.api';

import { extractErrorMessage } from '@/shared/lib';

interface Props {
  onCancel: () => void;
  onSaved: (llmModel: string) => void;
}

export const SystemAssistantSettingsForm = ({ onCancel, onSaved }: Props) => {
  const [models, setModels] = useState<string[]>([]);
  const [selectedModel, setSelectedModel] = useState<string>();
  const [unavailableCurrentModel, setUnavailableCurrentModel] = useState<string>();
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string>();
  const [updateLoading, setUpdateLoading] = useState(false);
  const activeRef = useRef(true);
  const mutationGenerationRef = useRef(0);

  useEffect(() => {
    let cancelled = false;
    activeRef.current = true;
    agentApi.getSystemSettings()
      .then((settings) => {
        if (cancelled) return;
        const available = settings.availableModels;
        const currentUnavailable = settings.llmModel && !available.includes(settings.llmModel);
        setModels(currentUnavailable ? [settings.llmModel, ...available] : available);
        setUnavailableCurrentModel(currentUnavailable ? settings.llmModel : undefined);
        setSelectedModel(settings.llmModel || undefined);
      })
      .catch((err) => {
        if (cancelled) return;
        const detail = extractErrorMessage(err, '加载助手模型失败');
        setLoadError(detail);
        message.error({ content: detail, duration: 0 });
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
      activeRef.current = false;
      mutationGenerationRef.current += 1;
    };
  }, []);

  const canSubmit = !!selectedModel && selectedModel !== unavailableCurrentModel && !loading && !loadError;
  const handleSave = async () => {
    if (!canSubmit || updateLoading) return;
    const mutationGeneration = ++mutationGenerationRef.current;
    setUpdateLoading(true);
    try {
      const settings = await agentApi.updateSystemSettings({ llmModel: selectedModel });
      if (!activeRef.current || mutationGeneration !== mutationGenerationRef.current) return;
      message.success({ content: '助手模型已更新', duration: 2 });
      onSaved(settings.llmModel);
    } catch (err) {
      if (activeRef.current && mutationGeneration === mutationGenerationRef.current) {
        message.error({ content: extractErrorMessage(err, '更新助手模型失败'), duration: 0 });
      }
    } finally {
      if (activeRef.current && mutationGeneration === mutationGenerationRef.current) {
        setUpdateLoading(false);
      }
    }
  };

  return (
    <Form layout="vertical" onFinish={handleSave}>
      <Typography.Paragraph type="secondary">
        平台负责助手的指令和工具边界，租户管理员只需选择对话模型。
      </Typography.Paragraph>
      {loadError && (
        <Alert type="error" showIcon message="加载助手模型失败" description={loadError} />
      )}
      <Form.Item label="助手模型" required style={{ marginTop: 16 }}>
        <Select
          aria-label="助手模型"
          value={selectedModel}
          onChange={setSelectedModel}
          loading={loading}
          disabled={loading || !!loadError}
          placeholder="请选择模型"
          options={models.map((model) => ({
            value: model,
            label: model === unavailableCurrentModel
              ? `${model}（当前不可用）`
              : model,
          }))}
          showSearch
          optionFilterProp="label"
        />
      </Form.Item>
      <div className="responsive-form-actions" style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
        <Button onClick={onCancel}>取消</Button>
        <Button type="primary" htmlType="submit" loading={updateLoading} disabled={!canSubmit}>
          保存修改
        </Button>
      </div>
    </Form>
  );
};
