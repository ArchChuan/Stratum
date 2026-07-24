import { ClockCircleOutlined, FileSearchOutlined } from '@ant-design/icons';
import { Collapse, Empty, List, Space, Tag, Typography } from 'antd';

import type { Citation, DiagnosticReport as DiagnosticReportModel } from '../model/agent';

const { Link, Text } = Typography;

interface Props {
  report: DiagnosticReportModel;
  profileVersion?: string;
}

const safeCitationURL = (value: string) => {
  try {
    const url = new URL(value);
    return url.protocol === 'https:' || url.protocol === 'http:' ? url.toString() : undefined;
  } catch {
    return undefined;
  }
};

const Section = ({ title, values }: { title: string; values: React.ReactNode[] }) => (
  <section style={{ minWidth: 0 }}>
    <Text strong style={{ display: 'block', marginBottom: 6 }}>{title}</Text>
    {values.length > 0 ? (
      <List
        size="small"
        dataSource={values}
        renderItem={(value) => <List.Item style={{ paddingInline: 0, minWidth: 0 }}>{value}</List.Item>}
      />
    ) : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={`${title}还是空的`} />}
  </section>
);

const CitationLink = ({ citation }: { citation: Citation }) => {
  const href = safeCitationURL(citation.url);
  const label = `${citation.title} · ${citation.section}`;
  return (
    <div style={{ minWidth: 0, overflowWrap: 'anywhere' }}>
      {href ? <Link href={href} target="_blank" rel="noreferrer">{label}</Link> : <Text>{label}</Text>}
      <Text type="secondary" style={{ display: 'block', fontSize: 12 }}>
        {citation.productVersion} · {citation.excerpt}
      </Text>
    </div>
  );
};

export const DiagnosticReport = ({ report, profileVersion }: Props) => {
  const evidenceCount = report.facts.length + report.evidenceGaps.length + report.citations.length;
  return (
    <div className="diagnostic-report" style={{ marginTop: 10, minWidth: 0, width: '100%' }}>
      <Collapse
        size="small"
        items={[{
          key: 'evidence',
          label: (
            <Space size={6} wrap>
              <FileSearchOutlined />
              <Text strong>诊断证据</Text>
              <Tag color="blue">{evidenceCount} 项</Tag>
              {profileVersion && <Text type="secondary" style={{ fontSize: 11 }}>{profileVersion}</Text>}
            </Space>
          ),
          children: (
            <div
              className="diagnostic-report-content"
              style={{ display: 'grid', gap: 16, minWidth: 0, overflowWrap: 'anywhere' }}
            >
              <Section title="已确认事实" values={report.facts.map((fact, index) => (
                <div key={`${fact.source}-${index}`}>
                  <Space size={6} wrap>
                    <Tag>{fact.area}</Tag>
                    <Text>{fact.statement}</Text>
                  </Space>
                  <Text type="secondary" style={{ display: 'block', fontSize: 12 }}>
                    来源：{fact.source}
                  </Text>
                </div>
              ))} />
              {report.inferences.length > 0 && (
                <Section title="分析判断" values={report.inferences.map((value) => <Text key={value}>{value}</Text>)} />
              )}
              <Section title="证据缺口" values={report.evidenceGaps.map((gap, index) => (
                <Space key={`${gap.source}-${gap.code}-${index}`} size={6} wrap>
                  {gap.area && <Tag color="orange">{gap.area}</Tag>}
                  <Text>{gap.code}</Text>
                  {gap.source && <Text type="secondary">来源：{gap.source}</Text>}
                </Space>
              ))} />
              <Section title="建议操作" values={report.recommendedActions.map((value) => <Text key={value}>{value}</Text>)} />
              <Section title="工具步骤与耗时" values={report.steps.map((step, index) => (
                <Space key={`${step.tool}-${index}`} size={6} wrap>
                  <Text>{step.tool}</Text>
                  <Tag color={step.outcome === 'success' ? 'green' : 'orange'}>{step.outcome}</Tag>
                  <Text type="secondary"><ClockCircleOutlined /> {step.latencyMs} ms</Text>
                  {step.errorCode && <Text type="danger">{step.errorCode}</Text>}
                </Space>
              ))} />
              <Section title="官方引用" values={report.citations.map((citation, index) => (
                <CitationLink key={`${citation.documentId}-${citation.section}-${index}`} citation={citation} />
              ))} />
            </div>
          ),
        }]}
      />
    </div>
  );
};
