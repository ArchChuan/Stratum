import { Tag } from 'antd';
import type { ModelCapability } from '../model/llm';

const CAP_COLORS: Record<ModelCapability, string> = {
  chat: 'blue',
  embedding: 'green',
  vision: 'purple',
  tool_use: 'orange',
  reasoning: 'red',
};

const CAP_LABELS: Record<ModelCapability, string> = {
  chat: '对话',
  embedding: '嵌入',
  vision: '视觉',
  tool_use: '工具调用',
  reasoning: '推理',
};

export function ModelCapabilityTags({ capabilities }: { capabilities: ModelCapability[] }) {
  return (
    <>
      {capabilities.map((cap) => (
        <Tag key={cap} color={CAP_COLORS[cap]}>
          {CAP_LABELS[cap]}
        </Tag>
      ))}
    </>
  );
}
