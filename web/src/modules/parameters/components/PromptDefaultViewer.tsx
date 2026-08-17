import { Button, Input, message, Modal, Typography } from 'antd';
import { useCallback, useState } from 'react';

import { parametersApi } from '../api/parameters.api';

import { extractErrorMessage } from '@/shared/lib';

const { Text } = Typography;
const { TextArea } = Input;

// 会话级缓存:同一 promptKey 只请求一次(模板全文是静态白名单)。
const templateCache = new Map<string, string>();

export interface PromptDefaultViewerProps {
  // 完整 registry key(如 "agent.compaction_prompt")。
  promptKey: string;
  label?: string;
}

// PromptDefaultViewer 在提示词表单项未设置时展示"查看默认提示词"入口,
// 弹 Modal 显示后端下发的默认模板全文并支持复制。只读展示,不写回表单。
export const PromptDefaultViewer = ({ promptKey, label = '查看默认提示词' }: PromptDefaultViewerProps) => {
  const [open, setOpen] = useState(false);
  const [template, setTemplate] = useState<string>();
  const [loading, setLoading] = useState(false);

  const openModal = useCallback(async () => {
    setOpen(true);
    const cached = templateCache.get(promptKey);
    if (cached !== undefined) {
      setTemplate(cached);
      return;
    }
    setLoading(true);
    try {
      const defaults = await parametersApi.promptDefaults();
      const content = defaults[promptKey];
      if (content === undefined) {
        throw new Error(`未找到默认提示词 ${promptKey}`);
      }
      templateCache.set(promptKey, content);
      setTemplate(content);
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '加载默认提示词失败'), duration: 0 });
      setOpen(false);
    } finally {
      setLoading(false);
    }
  }, [promptKey]);

  const copy = useCallback(() => {
    if (!template) return;
    navigator.clipboard
      .writeText(template)
      .then(() => message.success({ content: '默认提示词已复制', duration: 2 }));
  }, [template]);

  return (
    <>
      <Button type="link" size="small" style={{ padding: 0, height: 'auto' }} onClick={openModal}>
        {label}
      </Button>
      <Modal
        title="默认提示词模板"
        open={open}
        onCancel={() => setOpen(false)}
        footer={[
          <Button key="close" onClick={() => setOpen(false)}>
            关闭
          </Button>,
          <Button key="copy" type="primary" disabled={!template} onClick={copy}>
            复制全文
          </Button>,
        ]}
      >
        <Text type="secondary" style={{ display: 'block', marginBottom: 8, fontSize: 12 }}>
          {promptKey}（未配置时执行按此模板兜底；含 %s/%d 占位符，运行时替换）
        </Text>
        <TextArea
          value={template}
          readOnly
          rows={14}
          placeholder={loading ? '加载中…' : '暂无默认模板'}
          style={{ fontFamily: 'monospace', fontSize: 12 }}
        />
      </Modal>
    </>
  );
};
