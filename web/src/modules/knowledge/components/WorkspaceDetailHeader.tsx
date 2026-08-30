import { ArrowLeftOutlined } from '@ant-design/icons';
import { Button, Typography } from 'antd';

import { RequestEditorButton } from '@/shared/components';

const { Title, Text } = Typography;

interface WorkspaceDetailHeaderProps {
  name: string;
  description?: string;
  onBack: () => void;
  onDescriptionSave?: (desc: string) => void;
  onNameSave?: (name: string) => void;
  // 非白名单普通成员「申请编辑权限」入口；admin/owner 由调用方传 false/缺省。
  canRequestEditor?: boolean;
}

export const WorkspaceDetailHeader = ({
  name,
  description,
  onBack,
  onDescriptionSave,
  onNameSave,
  canRequestEditor = false,
}: WorkspaceDetailHeaderProps) => (
  <div className="responsive-detail-header" style={{ marginBottom: 20 }}>
    <Button icon={<ArrowLeftOutlined />} onClick={onBack} type="text">
      返回
    </Button>
    {canRequestEditor && (
      <RequestEditorButton
        resourceType="knowledge_workspace"
        resourceId={name}
        options={{ resourceName: name }}
        buttonProps={{ type: 'link', size: 'small' }}
      />
    )}
    <div className="long-text">
      <Title
        level={4}
        className="long-text"
        style={{ margin: 0 }}
        editable={onNameSave ? { onChange: onNameSave, tooltip: '编辑名称' } : false}
      >
        {name}
      </Title>
      <Text
        type="secondary"
        className="long-text"
        style={{ fontSize: 13 }}
        editable={onDescriptionSave ? { onChange: onDescriptionSave, tooltip: '编辑描述' } : false}
      >
        {description || '暂无描述'}
      </Text>
    </div>
  </div>
);
