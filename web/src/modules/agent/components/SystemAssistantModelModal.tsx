import { Alert, Modal, Select, Typography, message } from 'antd';
import { useEffect, useRef, useState } from 'react';

import { agentApi } from '../api/agent.api';

import { extractErrorMessage } from '@/shared/lib/errorMessage';

interface Props {
  open: boolean;
  canManage: boolean;
  onClose: () => void;
  onSaved: (llmModel: string) => void;
}

export const SystemAssistantModelModal = (props: Props) => {
  if (!props.open) return null;
  return <OpenSystemAssistantModelModal {...props} />;
};

const OpenSystemAssistantModelModal = ({ canManage, onClose, onSaved }: Props) => {
  const [models, setModels] = useState<string[]>([]);
  const [selectedModel, setSelectedModel] = useState<string>();
  const [loading, setLoading] = useState(true);
  const [loaded, setLoaded] = useState(false);
  const [loadError, setLoadError] = useState<string>();
  const [updateLoading, setUpdateLoading] = useState(false);
  const requestGenerationRef = useRef(0);
  const mutationGenerationRef = useRef(0);
  const activeRef = useRef(true);
  const canManageRef = useRef(canManage);
  canManageRef.current = canManage;

  useEffect(() => {
    activeRef.current = true;
    const requestGeneration = ++requestGenerationRef.current;
    let cancelled = false;
    setModels([]);
    setSelectedModel(undefined);
    setLoadError(undefined);
    setLoaded(false);
    setLoading(true);
    Promise.all([agentApi.models(), agentApi.getSystemSettings()])
      .then(([availableModels, settings]) => {
        if (cancelled || requestGeneration !== requestGenerationRef.current) return;
        setModels(availableModels);
        setSelectedModel(settings.llmModel || undefined);
        setLoaded(true);
      })
      .catch((err) => {
        if (cancelled || requestGeneration !== requestGenerationRef.current) return;
        const detail = extractErrorMessage(err, '加载助手模型失败');
        setLoadError(detail);
        message.error({ content: detail, duration: 0 });
      })
      .finally(() => {
        if (!cancelled && requestGeneration === requestGenerationRef.current) setLoading(false);
      });
    return () => {
      cancelled = true;
      activeRef.current = false;
      requestGenerationRef.current += 1;
      mutationGenerationRef.current += 1;
    };
  }, []);

  const canSubmit = canManage && loaded && !loading && !loadError && !!selectedModel && !updateLoading;
  const handleSave = async () => {
    if (!canSubmit || !selectedModel) return;
    const mutationGeneration = ++mutationGenerationRef.current;
    setUpdateLoading(true);
    try {
      const settings = await agentApi.updateSystemSettings({ llmModel: selectedModel });
      if (
        !activeRef.current ||
        !canManageRef.current ||
        mutationGeneration !== mutationGenerationRef.current
      ) return;
      onSaved(settings.llmModel);
      message.success({ content: '助手模型已更新', duration: 2 });
      onClose();
    } catch (err) {
      if (
        activeRef.current &&
        canManageRef.current &&
        mutationGeneration === mutationGenerationRef.current
      ) {
        message.error({ content: extractErrorMessage(err, '更新助手模型失败'), duration: 0 });
      }
    } finally {
      if (
        activeRef.current &&
        canManageRef.current &&
        mutationGeneration === mutationGenerationRef.current
      ) setUpdateLoading(false);
    }
  };

  return (
    <Modal
      title="设置助手模型"
      open
      onCancel={onClose}
      onOk={handleSave}
      okText="保存模型"
      cancelText="取消"
      confirmLoading={updateLoading}
      okButtonProps={{ disabled: !canSubmit }}
      destroyOnHidden
    >
      <Typography.Paragraph type="secondary">
        平台负责助手的指令和工具边界，租户只需选择对话模型。
      </Typography.Paragraph>
      {loadError && (
        <Alert
          type="error"
          showIcon
          message="加载助手模型失败"
          description={loadError}
          style={{ marginBottom: 12 }}
        />
      )}
      <Select
        aria-label="助手模型"
        value={selectedModel}
        onChange={setSelectedModel}
        loading={loading}
        disabled={loading || !!loadError || !canManage}
        placeholder="请选择模型"
        options={models.map((model) => ({ value: model, label: model }))}
        style={{ width: '100%' }}
        showSearch
        optionFilterProp="label"
      />
    </Modal>
  );
};
