// 默认值提示工具：unset 判定 + 展示格式化。表单值为空时只提示不写回，
// 0=unset 与缺失键不可区分（后端 PlatformValues 省略 0 默认键），故 0 由
// 调用方按字段语义决定是否视为未设置，这里只处理 undefined/null/''。

// isUnsetValue 判定未设置：undefined / null / 空串。0 不是 unset
// （temperature/compaction 等字段的 0 由调用方单独按语义处理）。
export function isUnsetValue(value: unknown): boolean {
  return value === undefined || value === null || value === '';
}

// formatDefaultValue 把默认值格式化为提示文案，null 表示无默认可展示。
// boolean 显示 开/关，字符串原样，数字直接显示。
export function formatDefaultValue(value: unknown): string | null {
  if (isUnsetValue(value)) return null;
  if (typeof value === 'boolean') return value ? '开' : '关';
  return String(value);
}
