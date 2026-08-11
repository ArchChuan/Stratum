import { InboxOutlined } from '@ant-design/icons';
import { Card, Select, Upload, message } from 'antd';
import { useState } from 'react';

import { KNOWLEDGE_MAX_UPLOAD_SIZE_BYTES } from '@/constants';
import type { Member } from '@/modules/iam';

const ACCEPTED_EXTENSIONS = ['txt', 'pdf', 'md', 'docx'];

export interface UploadAccessValues {
  allowedUserIDs: string[];
  allowedRoleIDs: string[];
}

interface WorkspaceUploadZoneProps {
  loading: boolean;
  // platform-managed 知识库豁免白名单机制：隐藏权限设置区，上传不携带白名单
  platformManaged?: boolean;
  userCandidates: Member[];
  userCandidatesLoading?: boolean;
  roleCandidates: string[];
  onUpload: (params: { file: File | Blob } & UploadAccessValues) => void;
}

export const WorkspaceUploadZone = ({
  loading,
  platformManaged = false,
  userCandidates,
  userCandidatesLoading = false,
  roleCandidates,
  onUpload,
}: WorkspaceUploadZoneProps) => {
  const [allowedUserIDs, setAllowedUserIDs] = useState<string[]>([]);
  const [allowedRoleIDs, setAllowedRoleIDs] = useState<string[]>([]);

  return (
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
          onUpload({ file, allowedUserIDs, allowedRoleIDs });
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
      {!platformManaged && (
        <div style={{ marginTop: 12 }}>
          <div style={{ fontSize: 12, color: '#595959', marginBottom: 6 }}>
            上传文档的访问权限（可选）：不设置 = 所有租户成员可查看
          </div>
          <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
            <Select
              mode="multiple"
              placeholder="指定用户"
              value={allowedUserIDs}
              onChange={setAllowedUserIDs}
              loading={userCandidatesLoading}
              style={{ minWidth: 200, flex: 1 }}
              maxTagCount="responsive"
            >
              {userCandidates.map((member) => (
                <Select.Option key={member.user_id} value={member.user_id}>
                  {member.github_login || member.user_id}
                </Select.Option>
              ))}
            </Select>
            <Select
              mode="multiple"
              placeholder="指定角色"
              value={allowedRoleIDs}
              onChange={setAllowedRoleIDs}
              style={{ minWidth: 140, flex: 1 }}
              maxTagCount="responsive"
            >
              {roleCandidates.map((role) => (
                <Select.Option key={role} value={role}>
                  {role}
                </Select.Option>
              ))}
            </Select>
          </div>
        </div>
      )}
    </Card>
  );
};
