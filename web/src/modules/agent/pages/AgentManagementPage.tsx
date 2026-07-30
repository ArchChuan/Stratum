import { Tabs } from 'antd';
import { useLocation, useNavigate } from 'react-router-dom';

import { AgentsListPage } from './AgentsListPage';
import { PlatformAssistantPage } from './PlatformAssistantPage';

export const AgentManagementPage = () => {
  const location = useLocation();
  const navigate = useNavigate();
  const activeKey = location.pathname === '/agents/list' ? 'list' : 'assistant';

  return (
    <Tabs
      activeKey={activeKey}
      onChange={(key) => navigate(key === 'list' ? '/agents/list' : '/agents')}
      items={[
        { key: 'assistant', label: '平台助手', children: <PlatformAssistantPage /> },
        { key: 'list', label: 'Agent 列表', children: <AgentsListPage /> },
      ]}
    />
  );
};
