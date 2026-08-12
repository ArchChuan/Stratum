import { Form, Modal, Select, type FormInstance } from 'antd';

import type { DocAccessValues } from '../model/knowledge';

import type { Member } from '@/modules/iam';

interface DocAccessModalProps {
  open: boolean;
  loading: boolean;
  form: FormInstance<DocAccessValues>;
  documentTitle: string;
  userCandidates: Member[];
  userCandidatesLoading?: boolean;
  roleCandidates: string[];
  onClose: () => void;
  onSubmit: (values: DocAccessValues) => void;
}

// DocAccessModal 编辑文档级访问白名单（P0.8）：任一维度非空即白名单生效
// （user_id ∈ allowedUserIDs OR tenant_role ∈ allowedRoleIDs）；两者全空 =
// 不限制（继承 workspace 可见性）。创建者与 admin/owner 隐式放行，无需列入。
export function DocAccessModal({
  open,
  loading,
  form,
  documentTitle,
  userCandidates,
  userCandidatesLoading = false,
  roleCandidates,
  onClose,
  onSubmit,
}: DocAccessModalProps) {
  return (
    <Modal
      className="mobile-overlay"
      title="设置文档访问权限"
      open={open}
      onCancel={() => {
        onClose();
        form.resetFields();
      }}
      onOk={() => form.submit()}
      okText="保存"
      cancelText="取消"
      confirmLoading={loading}
      width={480}
      destroyOnHidden
    >
      <Form form={form} layout="vertical" onFinish={onSubmit}>
        <Form.Item
          label="指定用户"
          name="allowedUserIDs"
          extra="白名单用户可查看此文档；管理员与创建者始终可见"
        >
          <Select
            mode="multiple"
            placeholder="选择可查看此文档的用户"
            allowClear
            loading={userCandidatesLoading}
            style={{ width: '100%' }}
            maxTagCount="responsive"
          >
            {userCandidates.map((member) => (
              <Select.Option key={member.user_id} value={member.user_id}>
                {member.github_login || member.user_id}
              </Select.Option>
            ))}
          </Select>
        </Form.Item>
        <Form.Item
          label="指定角色"
          name="allowedRoleIDs"
          extra="持有所选租户角色的成员均可查看此文档"
        >
          <Select
            mode="multiple"
            placeholder="选择角色"
            allowClear
            style={{ width: '100%' }}
            maxTagCount="responsive"
          >
            {roleCandidates.map((role) => (
              <Select.Option key={role} value={role}>
                {role}
              </Select.Option>
            ))}
          </Select>
        </Form.Item>
        <Form.Item style={{ marginBottom: 0 }}>
          <div style={{ fontSize: 12, color: '#8c8c8c' }}>
            两者均不选 = 所有租户成员可查看（当前文档「{documentTitle}」将取消限制）
          </div>
        </Form.Item>
      </Form>
    </Modal>
  );
}

export default DocAccessModal;
