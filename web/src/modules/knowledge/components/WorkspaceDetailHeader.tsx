import { ArrowLeftOutlined } from '@ant-design/icons';
import { Button, Typography } from 'antd';

const { Title, Text } = Typography;

interface WorkspaceDetailHeaderProps {
  name: string;
  description?: string;
  onBack: () => void;
  onDescriptionSave?: (desc: string) => void;
  onNameSave?: (name: string) => void;
}

export const WorkspaceDetailHeader = ({ name, description, onBack, onDescriptionSave, onNameSave }: WorkspaceDetailHeaderProps) => (
  <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 20 }}>
    <Button icon={<ArrowLeftOutlined />} onClick={onBack} type="text">
      返回
    </Button>
    <div>
      <Title
        level={4}
        style={{ margin: 0 }}
        editable={onNameSave ? { onChange: onNameSave, tooltip: '编辑名称' } : false}
      >
        {name}
      </Title>
      <Text
        type="secondary"
        style={{ fontSize: 13 }}
        editable={onDescriptionSave ? { onChange: onDescriptionSave, tooltip: '编辑描述' } : false}
      >
        {description || '暂无描述'}
      </Text>
    </div>
  </div>
);
