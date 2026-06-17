import { DatabaseOutlined } from '@ant-design/icons';
import { Button, Select, Space, Spin, Tag, Typography } from 'antd';
import { useState } from 'react';

import { EMBEDDING_MODEL_OPTIONS } from '@/constants';
import { SectionHeader } from '@/shared/ui';

const { Text } = Typography;

interface Props {
  embedModel: string;
  fetchLoading: boolean;
  embedLoading: boolean;
  canEditKeys: boolean;
  role?: string;
  onSave: (selected: string) => void;
}

export const TenantEmbeddingCard = ({
  embedModel,
  fetchLoading,
  embedLoading,
  canEditKeys,
  role,
  onSave,
}: Props) => {
  const [selected, setSelected] = useState('');
  return (
    <div
      style={{
        background: '#fff',
        borderRadius: 12,
        border: '1px solid #f0f0f0',
        padding: 24,
        height: '100%',
      }}
    >
      <SectionHeader
        icon={<DatabaseOutlined />}
        title="嵌入模型"
        subtitle="记忆系统向量化使用的 Embedding 模型，设置后不可更改"
      />
      <Spin spinning={fetchLoading}>
        {embedModel ? (
          <div>
            <Text type="secondary" style={{ fontSize: 13 }}>
              当前嵌入模型：
            </Text>
            <Tag color="blue" style={{ marginLeft: 8, fontSize: 13 }}>
              {embedModel}
            </Tag>
            <div style={{ marginTop: 8 }}>
              <Text type="secondary" style={{ fontSize: 12 }}>
                嵌入模型已设置，不支持修改（修改需重建所有向量索引）
              </Text>
            </div>
          </div>
        ) : (
          <div>
            <Text type="secondary" style={{ fontSize: 13, display: 'block', marginBottom: 12 }}>
              尚未配置嵌入模型，设置后将用于记忆与知识库的向量化，
              <Text strong style={{ color: '#fa8c16' }}>
                设置后不可更改
              </Text>
            </Text>
            <Space wrap>
              <Select
                style={{ width: 300 }}
                placeholder="选择嵌入模型"
                value={selected || undefined}
                onChange={setSelected}
                disabled={!canEditKeys}
                options={EMBEDDING_MODEL_OPTIONS}
              />
              <Button
                type="primary"
                loading={embedLoading}
                disabled={!canEditKeys || !selected}
                onClick={() => onSave(selected)}
              >
                确认设置
              </Button>
            </Space>
            {!canEditKeys && (
              <div style={{ marginTop: 8 }}>
                <Text type="secondary" style={{ fontSize: 12 }}>
                  当前角色（{role}）无权限修改
                </Text>
              </div>
            )}
          </div>
        )}
      </Spin>
    </div>
  );
};
