import { Typography } from 'antd';

import { formatDefaultValue } from '@/shared/lib/defaultValue';

const { Text } = Typography;

export interface DefaultHintProps {
  // 未设置时的默认值；null/undefined 时不渲染。
  value: unknown;
  // 默认值文案的可选前缀（如 "平台兜底"）；不传时显示 "默认：X"。
  label?: string;
  // 可选后缀（如 "（未设置）"），渲染在值之后。
  suffix?: string;
}

// DefaultHint 在表单控件未设置（使用系统默认）时展示默认值提示。
// 只展示不写回：表单值保持空/0，0=unset 语义不被破坏。
export const DefaultHint = ({ value, label = '默认', suffix = '' }: DefaultHintProps) => {
  const text = formatDefaultValue(value);
  if (text === null) return null;
  return <Text type="secondary">{`${label}：${text}${suffix}`}</Text>;
};
