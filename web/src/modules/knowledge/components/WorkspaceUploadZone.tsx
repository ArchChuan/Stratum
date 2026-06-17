import { InboxOutlined } from '@ant-design/icons';
import { Card, Upload } from 'antd';

interface WorkspaceUploadZoneProps {
  loading: boolean;
  onUpload: (params: { file: File | Blob }) => void;
}

export const WorkspaceUploadZone = ({ loading, onUpload }: WorkspaceUploadZoneProps) => (
  <Card
    title="上传文档"
    style={{ borderRadius: 12, border: '1px solid #f0f0f0', marginBottom: 16 }}
  >
    <Upload.Dragger
      beforeUpload={(file) => {
        onUpload({ file });
        return false;
      }}
      showUploadList={false}
      accept=".txt,.pdf,.md,.docx"
      style={{ padding: '12px 0' }}
      disabled={loading}
    >
      <p style={{ fontSize: 32, color: '#bfbfbf', marginBottom: 8 }}>
        <InboxOutlined />
      </p>
      <p style={{ fontSize: 14, color: '#262626', marginBottom: 4 }}>
        {loading ? '上传中...' : '点击或拖拽文件到此处上传'}
      </p>
      <p style={{ fontSize: 12, color: '#8c8c8c' }}>
        支持 .txt .pdf .md .docx，单文件最大 10MB
      </p>
    </Upload.Dragger>
  </Card>
);
