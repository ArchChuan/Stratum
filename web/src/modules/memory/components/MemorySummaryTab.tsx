import { Button, Card, Input, Space, Typography } from 'antd';

const { Text } = Typography;

interface MemorySummaryTabProps {
  sessionIdInput: string;
  setSessionIdInput: (v: string) => void;
  summary: string;
  onLoad: () => void;
}

export const MemorySummaryTab = ({
  sessionIdInput,
  setSessionIdInput,
  summary,
  onLoad,
}: MemorySummaryTabProps) => (
  <Space direction="vertical" style={{ width: '100%' }}>
    <Space>
      <Input
        value={sessionIdInput}
        onChange={(e) => setSessionIdInput(e.target.value)}
        placeholder="输入会话 ID"
        style={{ width: 300 }}
      />
      <Button onClick={onLoad} type="primary">
        生成摘要
      </Button>
    </Space>
    {summary && (
      <Card size="small">
        <Text>{summary}</Text>
      </Card>
    )}
  </Space>
);
