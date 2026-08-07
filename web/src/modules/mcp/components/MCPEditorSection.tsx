import { TeamOutlined } from '@ant-design/icons';
import { Form, Select, Typography } from 'antd';

import { mcpSectionStyle } from './mcpFormStyles';

import type { Member } from '@/modules/iam';
import { SectionHeader } from '@/shared/ui';

const { Option } = Select;
const { Text } = Typography;

const ROLE_LABELS: Record<string, string> = {
  owner: '超级管理员',
  admin: '管理员',
};

interface MCPEditorSectionProps {
  candidates: Member[];
  loading?: boolean;
}

export const MCPEditorSection = ({ candidates, loading }: MCPEditorSectionProps) => (
  <div style={mcpSectionStyle}>
    <SectionHeader icon={<TeamOutlined />} title="可编辑人" />
    <Form.Item
      label="可编辑人"
      name="editors"
      extra="可编辑人（租户管理员）可以修改此服务器配置；删除仍仅限创建者或超级管理员"
      style={{ marginBottom: 0 }}
    >
      <Select mode="multiple" loading={loading} placeholder="选择可编辑的管理员" allowClear>
        {candidates.map((member) => (
          <Option key={member.user_id} value={member.user_id}>
            {member.github_login || member.user_id}
            {member.role ? (
              <Text type="secondary" style={{ marginLeft: 6 }}>
                （{ROLE_LABELS[member.role] || member.role}）
              </Text>
            ) : null}
          </Option>
        ))}
      </Select>
    </Form.Item>
  </div>
);
