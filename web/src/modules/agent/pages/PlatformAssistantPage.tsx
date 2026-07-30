import { SettingOutlined } from '@ant-design/icons';
import { Button } from 'antd';
import { Link } from 'react-router-dom';

import { AgentChatPage } from './AgentChatPage';

import { useTenantRole } from '@/modules/iam';

export const PLATFORM_ASSISTANT_ID = 'stratum-platform-assistant';

export const PlatformAssistantPage = () => {
  const { isAdmin } = useTenantRole();
  return (
    <div>
      {isAdmin && (
        <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: 8 }}>
          <Link to={`/agents/${PLATFORM_ASSISTANT_ID}/edit`} aria-label="平台助手设置">
            <Button icon={<SettingOutlined />}>平台助手设置</Button>
          </Link>
        </div>
      )}
      <AgentChatPage
        fixedAgentId={PLATFORM_ASSISTANT_ID}
        showAgentSelector={false}
        embedded
      />
    </div>
  );
};
