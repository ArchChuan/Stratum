import { Input, InputNumber, Select, Slider, Switch } from 'antd';
import type { ReactNode } from 'react';

import type { ParameterDefinition, VisualHint } from '../model/parameters';

import { ModelSelect } from '@/modules/llm/components/ModelSelect';

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
//
// 受控属性必须转发：Form.Item 通过 cloneElement 把 value/onChange（toggle 为
// checked/onChange）注入到直接子组件。若不转发，控件永远显示空——平台参数页
// 刷新后"编辑参数变空"的根因（textarea/slider/select/number 均受影响）。
export const ParameterControl = ({
  def,
  value,
  checked,
  onChange,
}: {
  def: ParameterDefinition;
  value?: unknown;
  checked?: boolean;
  onChange?: (...args: unknown[]) => void;
}): ReactNode => {
  const hint: VisualHint = def.visual_hint;
  switch (hint.control) {
    case 'toggle':
      return <Switch checked={checked} onChange={onChange} />;
    case 'select': {
      const options = (hint.options ?? []).map((opt) => String(opt));
      return (
        <Select
          value={value as string}
          onChange={onChange}
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
      return (
        <Slider
          value={value as number}
          onChange={onChange}
          min={hint.min ?? 0}
          max={hint.max ?? 100}
          step={hint.step ?? 1}
          marks={sliderMarks(hint)}
        />
      );
    case 'number':
      return (
        <InputNumber
          value={value as number}
          onChange={onChange}
          min={hint.min ?? 0}
          max={hint.max ?? undefined}
          step={hint.step ?? 1}
          style={{ width: '100%', maxWidth: 240 }}
        />
      );
    case 'model':
      // 模型目录选择器（provider 分组）；存储值 = 模型名。
      return <ModelSelect value={value as string} onChange={onChange} />;
    case 'embedding_model':
      // 嵌入模型目录选择器（provider 分组）；存储值 = 模型名。
      return (
        <ModelSelect
          value={value as string}
          onChange={onChange}
          capability="embedding"
        />
      );
    case 'textarea':
      return (
        <TextArea
          value={value as string}
          onChange={onChange}
          rows={4}
          placeholder={typeof def.default === 'string' ? `默认：${def.default}` : undefined}
        />
      );
    default:
      return null;
  }
};
