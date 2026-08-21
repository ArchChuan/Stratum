import { Input, InputNumber, Select, Slider, Switch } from 'antd';
import type { ReactNode } from 'react';

import type { ParameterDefinition, VisualHint } from '../model/parameters';

import { ProviderModelSelect } from './ProviderModelSelect';

const { TextArea } = Input;
const { Option } = Select;

export const sliderMarks = (hint: VisualHint): Record<number, string> => {
  if (hint.max == null) return {};
  return {
    [hint.min ?? 0]: String(hint.min ?? 0),
    [hint.max]: String(hint.max),
  };
};

// ParameterControl 按定义的 visual_hint 渲染对应控件。空值 = 未设置（使用定义
// 默认）；textarea 的 placeholder 展示非空字符串默认值。
export const ParameterControl = ({ def }: { def: ParameterDefinition }): ReactNode => {
  const hint: VisualHint = def.visual_hint;
  switch (hint.control) {
    case 'toggle':
      return <Switch />;
    case 'select': {
      const options = (hint.options ?? []).map((opt) => String(opt));
      return (
        <Select
          style={{ width: '100%', maxWidth: 240 }}
          allowClear
          placeholder="未设置（使用定义默认）"
        >
          {options.map((opt) => (
            <Option key={opt} value={opt}>
              {opt}
            </Option>
          ))}
        </Select>
      );
    }
    case 'slider':
      return <Slider min={hint.min ?? 0} max={hint.max ?? 100} step={hint.step ?? 1} marks={sliderMarks(hint)} />;
    case 'number':
      return (
        <InputNumber
          min={hint.min ?? 0}
          max={hint.max ?? undefined}
          step={hint.step ?? 1}
          style={{ width: '100%', maxWidth: 240 }}
        />
      );
    case 'model':
      // 模型目录选择器（provider 分组）；存储值 = 模型名。
      return <ProviderModelSelect />;
    case 'embedding_model':
      // 嵌入模型目录选择器（provider 分组）；存储值 = 模型名。
      return <ProviderModelSelect capability="embedding" />;
    case 'textarea':
      return (
        <TextArea
          rows={4}
          placeholder={typeof def.default === 'string' ? `默认：${def.default}` : undefined}
        />
      );
    default:
      return null;
  }
};
