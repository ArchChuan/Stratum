import { DeleteOutlined, PlusOutlined } from '@ant-design/icons';
import { Button, Input, Select, Typography } from 'antd';
import { useState } from 'react';

import type { WorkflowNode } from '../model/workflow';

const { Text } = Typography;

/** 单行参数：key/value 落库；upstream/outputField 是「引用上游输出」编辑中的临时选择，不持久化。 */
interface ParamRow {
  key: string;
  value: string;
  upstream?: string;
  outputField: string;
}

const toRows = (mapping: Record<string, string>): ParamRow[] =>
  Object.entries(mapping || {}).map(([key, value]) => ({ key, value, outputField: '' }));

/** 持久化仅保留非空 key；空白行不落脏（契约 key 非空，空 key 会被后端保存校验拒绝）。 */
const toMapping = (rows: ParamRow[]): Record<string, string> => {
  const next: Record<string, string> = {};
  for (const row of rows) {
    const key = row.key.trim();
    if (key) next[key] = row.value;
  }
  return next;
};

/** 与后端 nodeInput 的 `nodes.<id>.output[.<key>]` 引用格式对齐。 */
const upstreamReference = (upstreamId: string, field: string): string =>
  `nodes.${upstreamId}.output.${field}`;

/**
 * 参数传递编辑器：按行编辑映射（输入映射/输出映射），每行 = 参数名 + 参数值文本。
 * 值支持自由文本或上游输出引用，引用通过「上游节点 + 输出字段」选择器拼写，避免手打长路径。
 * 文本草稿在组件内管理，编辑即通过 onChange 写回结构化 mapping（契约 z.record(z.string())）。
 */
export const ParameterMappingEditor = ({
  direction,
  mapping,
  upstreams,
  onChange,
}: {
  direction: 'input' | 'output';
  mapping: Record<string, string>;
  upstreams: WorkflowNode[];
  onChange: (next: Record<string, string>) => void;
}) => {
  const [rows, setRows] = useState<ParamRow[]>(() => toRows(mapping));
  // 后端只支持 agent/skill 节点做输出契约注入与失败重试（retryUpstreamOutput），
  // condition/mcp_tool/approval 的输出不是契约 JSON，引用其字段必然 run 失败，
  // 选择器只列可契约化的 agent/skill。
  const upstreamOptions = upstreams
    .filter((node) => node.type === 'agent' || node.type === 'skill')
    .map((node) => ({ value: node.id, label: node.name || node.id }));
  const update = (next: ParamRow[]) => {
    setRows(next);
    onChange(toMapping(next));
  };
  const insertReference = (index: number) => {
    const row = rows[index];
    if (!row.upstream || !row.outputField.trim()) return;
    const ref = upstreamReference(row.upstream, row.outputField.trim());
    // 后端 resolveMappingReference 只识别以 nodes. 开头的纯引用，混排文本
    // （如 "hello nodes.A.output.x"）会被整条静默丢弃，故值非空时整体替换。
    update(rows.map((r, i) => i === index ? { ...r, value: ref } : r));
  };
  const label = direction === 'input' ? '输入映射' : '输出映射';
  const valuePlaceholder = direction === 'input'
    ? '参数值，可引用上游 nodes.<节点>.output.<字段>'
    : '参数值，例如 {"summary":"$.result"}';

  return <section className="workflow-param-editor" aria-label={label}>
    <Text strong>{label}</Text>
    {rows.length === 0 && <Text type="secondary" className="workflow-param-empty">暂无映射，点击「添加参数」从上游传递数据。</Text>}
    {rows.map((row, index) => (
      <div className="workflow-param-row" key={index}>
        <div className="workflow-param-main">
          <Input
            aria-label={`${label}参数名`}
            placeholder="参数名"
            value={row.key}
            onChange={(event) => update(rows.map((r, i) => i === index ? { ...r, key: event.target.value } : r))}
          />
          <Input.TextArea
            aria-label={`${label}参数值`}
            placeholder={valuePlaceholder}
            rows={2}
            value={row.value}
            onChange={(event) => update(rows.map((r, i) => i === index ? { ...r, value: event.target.value } : r))}
          />
        </div>
        <div className="workflow-param-ref">
          {/* 上游输出引用是输入映射专属：输出映射契约是 $ / $.path JSONPath
              selector（validateOutputMappings），插入 nodes.<id>.output.<field>
              不合法，发布时会被拒，故 output 方向不渲染引用 UI。 */}
          {direction === 'input' && (
            <>
              <Select
                aria-label={`${label}引用上游节点`}
                options={upstreamOptions}
                placeholder="上游节点"
                value={row.upstream}
                disabled={upstreamOptions.length === 0}
                onChange={(upstream) => update(rows.map((r, i) => i === index ? { ...r, upstream } : r))}
              />
              <Input
                aria-label={`${label}引用输出字段`}
                placeholder="输出字段"
                value={row.outputField}
                onChange={(event) => update(rows.map((r, i) => i === index ? { ...r, outputField: event.target.value } : r))}
              />
              <Button
                size="small"
                disabled={!row.upstream || !row.outputField.trim()}
                onClick={() => insertReference(index)}
              >插入引用</Button>
            </>
          )}
          <Button
            aria-label={`${label}删除参数行`}
            danger
            icon={<DeleteOutlined />}
            size="small"
            type="text"
            onClick={() => update(rows.filter((_, i) => i !== index))}
          />
        </div>
      </div>
    ))}
    <Button
      block
      icon={<PlusOutlined />}
      onClick={() => update([...rows, { key: '', value: '', outputField: '' }])}
    >添加参数</Button>
  </section>;
};
