import { ArrowRightOutlined } from '@ant-design/icons';
import { Alert, Space, Typography } from 'antd';
import { Link } from 'react-router-dom';

interface SkillEvaluationPanelProps {
  skillId: string;
  stableRevisionId: string;
  isAdmin: boolean;
}

export const SkillEvaluationPanel = ({ skillId, stableRevisionId }: SkillEvaluationPanelProps) => {
  if (!stableRevisionId) {
    return <Alert type="warning" showIcon message="请先发布 Skill，再进行评测与优化。" />;
  }
  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      <Alert type="info" showIcon message="评测、候选生成与灰度实验已统一迁移到评测与进化中心。" />
      <Typography.Paragraph>当前入口已绑定此 Skill，可在中心查看历史并由管理员执行命令。</Typography.Paragraph>
      <Link aria-label="打开评测与进化中心" to={`/evaluations?kind=skill&resource_id=${encodeURIComponent(skillId)}`}>
        打开评测与进化中心 <ArrowRightOutlined />
      </Link>
    </Space>
  );
};
