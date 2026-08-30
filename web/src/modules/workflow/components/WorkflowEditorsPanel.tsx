import { Button, Select, Typography } from 'antd';
import { useEffect, useState } from 'react';

import { useEditorCandidates } from '@/modules/iam';

const { Text } = Typography;

interface WorkflowEditorsPanelProps {
  editors: string[];
  onSave: (editorIds: string[]) => Promise<void>;
}

// 工作流「可编辑人」白名单管理（admin/owner 可见）：多选成员 + 保存。
// 选项映射与 AgentFormSections「可编辑人」一致：value=user_id，label=github_login||user_id。
export function WorkflowEditorsPanel({ editors, onSave }: WorkflowEditorsPanelProps) {
  const { candidates, loading } = useEditorCandidates();
  const [editorIds, setEditorIds] = useState<string[]>(editors);
  const [saving, setSaving] = useState(false);

  useEffect(() => { setEditorIds(editors); }, [editors]);

  const save = async () => {
    setSaving(true);
    try {
      await onSave(editorIds);
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="workflow-editors-panel" style={{ marginBottom: 16 }}>
      <Text strong>可编辑人</Text>
      <Select
        mode="multiple"
        style={{ width: '100%', marginTop: 8 }}
        placeholder="选择可编辑的成员"
        allowClear
        loading={loading}
        value={editorIds}
        onChange={setEditorIds}
      >
        {candidates.map((member) => (
          <Select.Option key={member.user_id} value={member.user_id}>
            {member.github_login || member.user_id}
          </Select.Option>
        ))}
      </Select>
      <Button type="primary" size="small" aria-label="保存" loading={saving} onClick={() => { void save(); }} style={{ marginTop: 8 }}>保存</Button>
    </div>
  );
}
