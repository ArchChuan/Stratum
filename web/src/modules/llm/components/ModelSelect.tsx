import { Select, message } from 'antd';
import { useEffect, useState } from 'react';
import type { ReactNode } from 'react';

import type { GroupedModelOption } from '../model/grouped';
import { buildGroupedModels } from '../model/grouped';

import { filterModelOption, ModelOptionLabel } from './ModelOptionLabel';

import { llmApi } from '@/modules/llm';
import { extractErrorMessage } from '@/shared/lib';

const { Option, OptGroup } = Select;

export interface ModelSelectProps {
  /** Form.Item 注入的 id：透传给内部 Select 落到 input 上，label htmlFor 才能关联。 */
  id?: string;
  disabled?: boolean;
  value?: string;
  onChange?: (value: string) => void;
  placeholder?: string;
  /** 默认 true（沿用平台参数页行为，空值=未设置使用定义默认）；Agent 表单必选传 false。 */
  allowClear?: boolean;
  notFoundContent?: ReactNode;
  /** 决定内部拉取的目录能力范围；受控模式（传 groupedModels）时忽略。 */
  capability?: 'chat' | 'embedding';
  /** 传入则受控：不内部拉取，直接渲染。Agent 表单用它做联动（contextWindow/推理推断）。 */
  groupedModels?: GroupedModelOption[];
}

// 平台 Agent 表单、知识库编辑/新建、平台参数配置共用的模型选择器：按能力从
// /admin/models + /admin/providers 拉全量目录（含 disabled 模型），按 provider
// 分组渲染，健康徽章恒开，enabled=false 或健康不可用（unhealthy/half_open）的
// 模型显示但禁用不可选（fail-closed）。存储值 = 模型名（字符串），与后端取值
// 语义一致；写入校验目录存在性由后端 ValidateFn 承担。空值 = 未设置。
export const ModelSelect = ({
  id,
  disabled,
  value,
  onChange,
  placeholder = '未设置（使用定义默认）',
  allowClear = true,
  notFoundContent,
  capability = 'chat',
  groupedModels: controlledModels,
}: ModelSelectProps) => {
  const [fetchedModels, setFetchedModels] = useState<GroupedModelOption[]>([]);
  const groupedModels = controlledModels ?? fetchedModels;

  useEffect(() => {
    if (controlledModels) return;
    let cancelled = false;
    (async () => {
      const [modelsRes, providersRes] = await Promise.allSettled([
        llmApi.listModels({ capability }),
        llmApi.listProviders(),
      ]);
      if (cancelled) return;
      if (modelsRes.status === 'fulfilled' && providersRes.status === 'fulfilled') {
        setFetchedModels(buildGroupedModels(modelsRes.value, providersRes.value));
      } else {
        const failed = [modelsRes, providersRes].find((r) => r.status === 'rejected');
        if (failed && failed.status === 'rejected') {
          message.error({ content: extractErrorMessage(failed.reason, '加载模型目录失败'), duration: 3 });
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [capability, controlledModels]);

  // 当前值不在目录（退役/未发现）时保留显示为 disabled Option，避免 placeholder
  // 显示"未设置"却隐藏提交值。
  const currentMissing =
    value && !groupedModels.some((g) => g.models.some((m) => m.value === value));

  // 不可用模型禁用：enabled=false 平台停用；健康 unhealthy/half_open fail-closed
  // 不选中（与后端解析链 isModelUsable 语义一致），防止把熔断模型配成默认；
  // degraded 仍可选（已降级但可用）。
  const isUnusable = (health?: string) => health === 'unhealthy' || health === 'half_open';

  return (
    <Select
      id={id}
      disabled={disabled}
      value={value}
      onChange={onChange}
      allowClear={allowClear}
      placeholder={placeholder}
      // 选择框只显示模型名（option.label）；下拉仍渲染完整子元素（健康徽章/能力标签/「当前不可用」后缀）
      optionLabelProp="label"
      notFoundContent={notFoundContent}
      showSearch
      filterOption={filterModelOption}
      style={{ width: '100%', maxWidth: 360 }}
    >
      {currentMissing && (
        <Option key={value} value={value} label={value} disabled>
          {value}（当前不可用）
        </Option>
      )}
      {groupedModels.map((group) => (
        <OptGroup key={group.provider} label={group.provider}>
          {group.models.map((m) => (
            <Option
              key={m.value}
              value={m.value}
              label={m.label}
              disabled={m.enabled === false || isUnusable(m.health)}
            >
              <ModelOptionLabel label={m.label} capabilities={m.capabilities} health={m.health} showHealth />
            </Option>
          ))}
        </OptGroup>
      ))}
    </Select>
  );
};
