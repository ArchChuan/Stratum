import { Select, message } from 'antd';
import { useEffect, useState } from 'react';

import type { GroupedModelOption } from '@/modules/agent/model/agent';
import { buildGroupedModels } from '@/modules/agent/model/agent';
import { llmApi } from '@/modules/llm';
import { extractErrorMessage } from '@/shared/lib';

const { Option, OptGroup } = Select;

interface ProviderModelSelectProps {
  value?: string;
  onChange?: (value: string) => void;
  placeholder?: string;
}

// 平台参数页的模型选择器：从模型管理目录拉取 chat 模型，按 provider 分组渲染
//（先选 provider 再选模型）。存储值 = 模型名（字符串），与后端取值语义一致；
// 写入校验目录存在性由后端 ValidateFn 承担（fail-closed）。空值 = 未设置，
// 使用定义默认。
export const ProviderModelSelect = ({
  value,
  onChange,
  placeholder = '未设置（使用定义默认）',
}: ProviderModelSelectProps) => {
  const [groupedModels, setGroupedModels] = useState<GroupedModelOption[]>([]);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      const [modelsRes, providersRes] = await Promise.allSettled([
        llmApi.listModels({ capability: 'chat' }),
        llmApi.listProviders(),
      ]);
      if (cancelled) return;
      if (modelsRes.status === 'fulfilled' && providersRes.status === 'fulfilled') {
        setGroupedModels(buildGroupedModels(modelsRes.value, providersRes.value));
      } else {
        const failed = [modelsRes, providersRes].find((r) => r.status === 'rejected');
        if (failed && failed.status === 'rejected') {
          message.error({ content: extractErrorMessage(failed.reason, '加载模型目录失败'), duration: 0 });
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  // 当前值不在目录（退役/未发现）时保留显示为 disabled Option，避免 placeholder
  // 显示"未设置"却隐藏提交值；与 AgentFormSections currentModel 模式一致。
  const currentMissing =
    value && !groupedModels.some((g) => g.models.some((m) => m.value === value));

  return (
    <Select
      value={value}
      onChange={onChange}
      allowClear
      placeholder={placeholder}
      showSearch
      optionFilterProp="children"
      style={{ width: '100%', maxWidth: 360 }}
    >
      {currentMissing && (
        <Option key={value} value={value} disabled>
          {value}（当前不可用）
        </Option>
      )}
      {groupedModels.map((group) => (
        <OptGroup key={group.provider} label={group.provider}>
          {group.models.map((m) => (
            <Option key={m.value} value={m.value}>
              {m.label}
            </Option>
          ))}
        </OptGroup>
      ))}
    </Select>
  );
};
