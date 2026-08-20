import { ArrowRightOutlined } from '@ant-design/icons';
import { Alert, Button, message, Space, Typography } from 'antd';
import { useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';

import { evaluationApi } from '../api/evaluation.api';

import { extractErrorMessage } from '@/shared/lib';

interface SkillEvaluationPanelProps {
  skillId: string;
  stableRevisionId: string;
  isAdmin: boolean;
}

export const SkillEvaluationPanel = ({ skillId, stableRevisionId, isAdmin }: SkillEvaluationPanelProps) => {
  const navigate = useNavigate();
  const [baselineLoading, setBaselineLoading] = useState(false);
  const centerPath = `/evaluations?kind=skill&resource_id=${encodeURIComponent(skillId)}`;
  if (!stableRevisionId) {
    return <Alert type="warning" showIcon message="请先发布 Skill，再进行评测与优化。" />;
  }
  const registerBaseline = async () => {
    setBaselineLoading(true);
    try {
      await evaluationApi.createBaseline('skill', skillId);
      navigate(centerPath);
    } catch (error) {
      message.error({ content: extractErrorMessage(error) || '建立评测基线失败', duration: 3 });
    } finally {
      setBaselineLoading(false);
    }
  };
  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      <Alert type="info" showIcon message="评测、候选生成与灰度实验已统一迁移到评测与进化中心。" />
      <Typography.Paragraph>当前入口已绑定此 Skill，可在中心查看历史并由管理员执行命令。</Typography.Paragraph>
      {isAdmin ? <Button type="primary" aria-label="建立评测基线并打开中心" loading={baselineLoading}
        onClick={() => void registerBaseline()}>
        建立评测基线并打开中心 <ArrowRightOutlined />
      </Button> : <Link aria-label="打开评测与进化中心" to={centerPath}>
          打开评测与进化中心 <ArrowRightOutlined />
        </Link>}
    </Space>
  );
};
