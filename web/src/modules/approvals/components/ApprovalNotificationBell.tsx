import { BellOutlined } from '@ant-design/icons';
import { Badge, Dropdown, Empty, Typography } from 'antd';
import type { MenuProps } from 'antd';
import { useNavigate } from 'react-router-dom';

import { useApprovalNotifications } from '../hooks/useApprovalNotifications';

const HEADER_KEY = '__header__';
const EMPTY_KEY = '__empty__';

// 顶栏审批通知铃铛：角标 = 当前待审批数量（工具审批 + 权限提案之和）；点击展开下拉
// 预览最近几条，点击条目进入审批中心对应 tab（工具审批 → /approvals，权限提案 →
// /approvals?tab=permission）。对话页不再渲染审批操作卡片（M4/D4 产品决策：待办收敛
// 到铃铛 + 工作台）。
export const ApprovalNotificationBell = () => {
  const { items } = useApprovalNotifications();
  const navigate = useNavigate();

  const menuItems: NonNullable<MenuProps['items']> = [
    {
      key: HEADER_KEY,
      disabled: true,
      label: (
        <Typography.Text strong style={{ display: 'block', padding: '6px 12px' }}>
          待审批
        </Typography.Text>
      ),
    },
    ...(items.length === 0
      ? [
          {
            key: EMPTY_KEY,
            disabled: true,
            label: (
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description="暂无待审批"
                style={{ padding: '12px 24px', margin: 0 }}
              />
            ),
          },
        ]
      : items.slice(0, 5).map((item) => ({
          key: item.key,
          label: (
            <div style={{ padding: '6px 12px', minWidth: 240 }}>
              <div>{item.title}</div>
              <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                {item.subtitle}
              </Typography.Text>
            </div>
          ),
        }))),
  ];

  const handleClick: MenuProps['onClick'] = ({ key }) => {
    if (key === HEADER_KEY || key === EMPTY_KEY) return;
    const target = items.find((item) => item.key === key);
    navigate(target?.tab === 'permission' ? '/approvals?tab=permission' : '/approvals');
  };

  return (
    <Dropdown menu={{ items: menuItems, onClick: handleClick }} placement="bottomRight" trigger={['click']}>
      <Badge count={items.length} size="small" offset={[-2, 2]}>
        <BellOutlined
          style={{ fontSize: 18, cursor: 'pointer', padding: 8 }}
          aria-label="审批通知"
          title="审批通知"
        />
      </Badge>
    </Dropdown>
  );
};

export default ApprovalNotificationBell;
