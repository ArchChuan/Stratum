import { InboxOutlined } from '@ant-design/icons';
import { Card, Upload, message } from 'antd';

import { KNOWLEDGE_MAX_UPLOAD_SIZE_BYTES } from '@/constants';

const ACCEPTED_EXTENSIONS = ['txt', 'pdf', 'md', 'docx'];

interface WorkspaceUploadZoneProps {
  loading: boolean;
  onUpload: (params: { file: File | Blob }) => void;
}

export const WorkspaceUploadZone = ({ loading, onUpload }: WorkspaceUploadZoneProps) => (
  <Card
    className="responsive-detail-section"
    title="上传文档"
    style={{ borderRadius: 12, border: '1px solid #f0f0f0', marginBottom: 16 }}
  >
    <Upload.Dragger
      beforeUpload={(file) => {
        const ext = file.name.split('.').pop()?.toLowerCase() ?? '';
        if (!ACCEPTED_EXTENSIONS.includes(ext)) {
          message.error({ content: '仅支持 .txt .pdf .md .docx 文件', duration: 0 });
          return Upload.LIST_IGNORE;
        }
        if (file.size > KNOWLEDGE_MAX_UPLOAD_SIZE_BYTES) {
          message.error({ content: '单文件不能超过 10MB', duration: 0 });
          return Upload.LIST_IGNORE;
        }
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
