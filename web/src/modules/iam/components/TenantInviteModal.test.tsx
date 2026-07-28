import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { TenantInviteModal } from './TenantInviteModal';

describe('TenantInviteModal', () => {
  it('shows the one-time code after invitation creation', () => {
    const onCancel = vi.fn();
    render(
      <TenantInviteModal
        open
        loading={false}
        invitationCode="one-time-code"
        onCancel={onCancel}
        onSubmit={vi.fn()}
      />,
    );

    expect(screen.getByText('请立即复制邀请码')).toBeInTheDocument();
    expect(screen.getByText('one-time-code')).toBeInTheDocument();
    expect(screen.queryByLabelText('邮箱')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /完\s*成/ }));
    expect(onCancel).toHaveBeenCalledTimes(1);
  });
});
