import { ReloadOutlined } from '@ant-design/icons';
import { Button, Space, Typography } from 'antd';

import { ExecutionHistoryTable } from '../components/ExecutionHistoryTable';
import { useExecutionHistory } from '../hooks/useExecutionHistory';

import { DEFAULT_PAGE_SIZE } from '@/constants';

const { Title, Text } = Typography;

export const ExecutionHistoryPage = () => {
  const { executions, loading, pagination, fetchPage } = useExecutionHistory();

  return (
    <div>
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          marginBottom: 20,
        }}
      >
        <div>
          <Title level={4} style={{ margin: 0 }}>
            执行历史
          </Title>
          <Text type="secondary" style={{ fontSize: 13 }}>
            近期所有 Agent 执行记录
          </Text>
        </div>
        <Space>
          <Button
            icon={<ReloadOutlined />}
            onClick={() => fetchPage(1, pagination.pageSize)}
            loading={loading}
          >
            刷新
          </Button>
        </Space>
      </div>

      <ExecutionHistoryTable
        executions={executions}
        loading={loading}
        pagination={pagination}
        onChange={(pag) => fetchPage(pag.current || 1, pag.pageSize || DEFAULT_PAGE_SIZE)}
      />
    </div>
  );
};
