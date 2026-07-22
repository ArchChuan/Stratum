import { ArrowRightOutlined } from '@ant-design/icons';
import { Alert, Button, Input, Space, Typography, message } from 'antd';
import { useState } from 'react';
import { Link } from 'react-router-dom';

import { evaluationApi } from '../api/evaluation.api';
import type { EvaluationRun, ResourceRef, SuiteRevision } from '../model/evaluation';

import { EVALUATION_JOB_MAX_WAIT_MS, EVALUATION_JOB_POLL_INTERVAL_MS } from '@/constants';
import { extractErrorMessage } from '@/shared/lib';

const { Text, Paragraph } = Typography;
const { TextArea } = Input;

interface SkillEvaluationPanelProps {
  skillId: string;
  stableRevisionId: string;
  isAdmin: boolean;
}

export const SkillEvaluationPanel = ({ skillId, stableRevisionId, isAdmin }: SkillEvaluationPanelProps) => {
  const [input, setInput] = useState('{"input":"ping"}');
  const [expectedOutput, setExpectedOutput] = useState('E2E_PASS');
  const [suiteRevision, setSuiteRevision] = useState<SuiteRevision | null>(null);
  const [stableRun, setStableRun] = useState<EvaluationRun | null>(null);
  const [createLoading, setCreateLoading] = useState(false);
  const [actionLoading, setActionLoading] = useState('');

  const createSuite = async () => {
    setCreateLoading(true);
    try {
      const created = await evaluationApi.createSuite({
        name: `Skill ${skillId} 回归评测`,
        cases: [{
          name: '核心输出', input: parseInput(input), expected_output: expectedOutput,
          assertion_mode: 'contains', enabled: true,
        }],
      });
      const published = await evaluationApi.publishSuite(created.suite.id);
      setSuiteRevision(published);
      message.success({ content: '评测集已创建并发布', duration: 2 });
    } catch (err) {
      message.error({ content: extractErrorMessage(err) || '创建评测集失败', duration: 0 });
    } finally {
      setCreateLoading(false);
    }
  };

  const runEvaluation = async (revisionId: string) => {
    if (!suiteRevision) throw new Error('请先创建评测集');
    const resource: ResourceRef = { kind: 'skill', resource_id: skillId, revision_id: revisionId };
    const job = await evaluationApi.enqueueRun(resource, suiteRevision.id, `${revisionId}-${Date.now()}`);
    const deadline = Date.now() + EVALUATION_JOB_MAX_WAIT_MS;
    let current = job;
    while (current.status === 'queued' || current.status === 'running') {
      if (Date.now() >= deadline) throw new Error('评测任务等待超时');
      if (current.status === 'running') await delay(EVALUATION_JOB_POLL_INTERVAL_MS);
      current = await evaluationApi.getJob(current.job_id);
    }
    if (current.status !== 'succeeded' || !current.result_id) {
      throw new Error(current.error_message || '评测任务失败');
    }
    return evaluationApi.getRun(current.result_id);
  };

  const perform = async (name: string, operation: () => Promise<void>) => {
    setActionLoading(name);
    try {
      await operation();
    } catch (err) {
      message.error({ content: extractErrorMessage(err) || '操作失败', duration: 0 });
    } finally {
      setActionLoading('');
    }
  };

  const runStable = () => perform('stable', async () => {
    setStableRun(await runEvaluation(stableRevisionId));
  });

  if (!stableRevisionId) {
    return <Alert type="warning" showIcon message="请先发布 Skill，再进行评测与优化。" />;
  }

  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      <Alert type="info" showIcon message="当前评测绑定已发布的不可变 Skill revision。" />
      <Link
        aria-label="打开评测与进化中心"
        to={`/evaluations?kind=skill&resource_id=${encodeURIComponent(skillId)}`}
      >
        打开评测与进化中心 <ArrowRightOutlined />
      </Link>
      <Text strong>评测输入</Text>
      <TextArea aria-label="评测输入" rows={4} value={input} onChange={(event) => setInput(event.target.value)} />
      <Text strong>期望包含</Text>
      <Input aria-label="期望包含" value={expectedOutput} onChange={(event) => setExpectedOutput(event.target.value)} />
      {isAdmin && (
        <Button type="primary" loading={createLoading} onClick={createSuite}>
          创建并发布评测集
        </Button>
      )}
      {suiteRevision && <Paragraph>Suite revision：{suiteRevision.id}</Paragraph>}
      {suiteRevision && isAdmin && (
        <Space wrap>
          <Button loading={actionLoading === 'stable'} onClick={runStable}>运行基线评测</Button>
        </Space>
      )}
      {stableRun && <RunStatus label="基线评测" run={stableRun} />}
    </Space>
  );
};

const RunStatus = ({ label, run }: { label: string; run: EvaluationRun }) => (
  <Paragraph>{`${label}：${run.passed ? '通过' : '失败'}（${run.passed_cases}/${run.total_cases}）`}</Paragraph>
);

const delay = (milliseconds: number) => new Promise((resolve) => window.setTimeout(resolve, milliseconds));

const parseInput = (raw: string): unknown => {
  try {
    return JSON.parse(raw);
  } catch {
    return raw;
  }
};
