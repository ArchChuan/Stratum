import { Tag } from 'antd';

import type { ChatStep } from '../model/agent';

interface Props {
  steps?: ChatStep[];
}

export const ChatStepList = ({ steps }: Props) => {
  if (!steps?.length) return null;
  return (
    <div
      style={{
        marginTop: 8,
        paddingTop: 8,
        borderTop: '1px solid #f0f0f0',
        fontSize: 12,
        color: '#595959',
      }}
    >
      <b style={{ fontSize: 11, textTransform: 'uppercase', letterSpacing: 0.5 }}>执行步骤</b>
      <ol style={{ margin: '4px 0 0', paddingInlineStart: 20 }}>
        {steps.map((s, i) => (
          <li key={i}>
            <b>步骤 {(s as ChatStep & { iteration?: number }).iteration ?? i + 1}</b>：
            {(s as ChatStep & { action?: string }).action || s.type}
            {s.tool && <Tag style={{ marginLeft: 4, fontSize: 10 }}>{s.tool}</Tag>}
          </li>
        ))}
      </ol>
    </div>
  );
};
