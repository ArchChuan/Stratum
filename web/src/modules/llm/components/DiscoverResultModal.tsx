import { Modal, Table, Typography } from 'antd';
import { useEffect, useState } from 'react';

import type { Model, ModelCapability } from '../model/llm';

import { ModelCapabilityTags } from './ModelCapabilityTags';

const { Text } = Typography;

interface Props {
  open: boolean;
  onClose: () => void;
  results: Model[];
  providerName: string;
}

export function DiscoverResultModal({ open, onClose, results, providerName }: Props) {
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const safeResults: Model[] = results ?? [];

  useEffect(() => { setExpanded(new Set()); }, [results]);

  const columns = [
    { title: '模型名称', dataIndex: 'name', key: 'name', width: 200 },
    {
      title: '能力',
      dataIndex: 'capabilities',
      key: 'capabilities',
      render: (caps: ModelCapability[]) => <ModelCapabilityTags capabilities={caps} />,
    },
    {
      title: '上下文',
      dataIndex: 'contextWindow',
      key: 'contextWindow',
      width: 100,
      render: (v: number) => (v ? v.toLocaleString() : '-'),
    },
  ];

  return (
    <Modal
      title={`发现模型 — ${providerName}`}
      open={open}
      onCancel={onClose}
      onOk={onClose}
      width={640}
      footer={(_, { OkBtn }) => <OkBtn />}
    >
      <div style={{ marginBottom: 16 }}>
        <Text>
          共发现 <Text strong>{safeResults.length}</Text> 个模型
        </Text>
      </div>
      <Table
        dataSource={safeResults}
        columns={columns}
        rowKey="id"
        pagination={false}
        size="small"
        expandable={{
          expandedRowRender: (record) => (
            <div style={{ padding: '8px 0' }}>
              <p>
                <Text type="secondary">显示名称：</Text>
                {record.displayName || '-'}
              </p>
              <p>
                <Text type="secondary">最大输出：</Text>
                {record.maxTokens ? `${record.maxTokens.toLocaleString()} tokens` : '-'}
              </p>
              <p>
                <Text type="secondary">输入价格：</Text>
                {record.inputPrice != null ? `$${record.inputPrice}/1M tokens` : '-'}
              </p>
              <p>
                <Text type="secondary">输出价格：</Text>
                {record.outputPrice != null ? `$${record.outputPrice}/1M tokens` : '-'}
              </p>
            </div>
          ),
          expandedRowKeys: [...expanded],
          onExpand: (exp, record) => {
            const next = new Set(expanded);
            if (exp) {
              next.add(record.id);
            } else {
              next.delete(record.id);
            }
            setExpanded(next);
          },
        }}
      />
    </Modal>
  );
}
