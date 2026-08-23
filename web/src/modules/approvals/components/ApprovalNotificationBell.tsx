import { BellOutlined } from '@ant-design/icons';
import { Badge, Dropdown, Empty, Typography } from 'antd';
import type { MenuProps } from 'antd';
import { useNavigate } from 'react-router-dom';

import { useApprovalNotifications } from '../hooks/useApprovalNotifications';
import { riskLevelLabel, statusLabel } from '../labels';

const HEADER_KEY = '__header__';
const EMPTY_KEY = '__empty__';

// 顶栏审批通知铃铛：角标 = 当前待审批数量；点击展开下拉预览最近几条
// （工具名 + 风险 + 状态 + 过期时间），点击任意条目进入审批中心（唯一处理入口）。
// 对话页不再渲染审批操作卡片（M4/D4 产品决策：待办收敛到铃铛 + 工作台）。
export const ApprovalNotificationBell = () => {
  const { rows } = useApprovalNotifications();
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
    ...(rows.length === 0
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
      : rows.slice(0, 5).map((row) => ({
          key: row.id,
          label: (
            <div style={{ padding: '6px 12px', minWidth: 240 }}>
              <div>
                {row.tool_name}
                {row.server_id && (
                  <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                    {' '}
                    · {row.server_id}
                  </Typography.Text>
                )}
              </div>
              <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                {riskLevelLabel(row.risk_level)} · {statusLabel(row.status)} ·{' '}
                {new Date(row.expires_at).toLocaleString()}
              </Typography.Text>
            </div>
          ),
        }))),
  ];

  const handleClick: MenuProps['onClick'] = ({ key }) => {
    if (key === HEADER_KEY || key === EMPTY_KEY) return;
    navigate('/approvals');
  };

  return (
    <Dropdown menu={{ items: menuItems, onClick: handleClick }} placement="bottomRight" trigger={['click']}>
      <Badge count={rows.length} size="small" offset={[-2, 2]}>
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
