import { SendOutlined } from '@ant-design/icons';
import { Button, Card, Form, Input, type FormInstance } from 'antd';

import type { QueryResult } from '../model/knowledge';

import { WorkspaceQueryResult } from './WorkspaceQueryResult';

const { TextArea } = Input;

interface QueryValues {
  question: string;
}

interface WorkspaceQueryPanelProps {
  form: FormInstance<QueryValues>;
  loading: boolean;
  result: QueryResult | null;
  onSubmit: (values: QueryValues) => void;
}

export const WorkspaceQueryPanel = ({
  form,
  loading,
  result,
  onSubmit,
}: WorkspaceQueryPanelProps) => (
  <Card className="responsive-detail-section" title="查询测试" style={{ borderRadius: 12, border: '1px solid #f0f0f0' }}>
    <Form form={form} onFinish={onSubmit}>
      <Form.Item name="question" rules={[{ required: true, message: '请输入问题' }]}>
        <TextArea rows={3} placeholder="输入查询问题..." />
      </Form.Item>
      <Form.Item style={{ marginBottom: result ? 16 : 0 }}>
        <Button className="mobile-full-width" type="primary" htmlType="submit" icon={<SendOutlined />} loading={loading}>
          查询
        </Button>
      </Form.Item>
    </Form>

    {result && <WorkspaceQueryResult result={result} />}
  </Card>
);
