import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { authApi } from '../../api/auth.api';
import { CallbackPage } from './CallbackPage';

const login = vi.fn();

vi.mock('../../api/auth.api', () => ({
  authApi: {
    exchangeOAuth: vi.fn(),
    me: vi.fn(),
  },
}));

vi.mock('../../components/AuthContext', () => ({
  useAuth: () => ({ login }),
}));

const LocationProbe = () => {
  const location = useLocation();
  return <pre>{JSON.stringify({ pathname: location.pathname, search: location.search, state: location.state })}</pre>;
};

describe('CallbackPage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    sessionStorage.clear();
  });

  it('exchanges code and carries onboarding credentials in router memory only', async () => {
    vi.mocked(authApi.exchangeOAuth).mockResolvedValue({
      kind: 'onboarding',
      onboarding_token: 'onboarding-token',
      github_login: 'octocat',
      avatar_url: 'https://example.test/avatar',
    });
    const storageSpy = vi.spyOn(Storage.prototype, 'setItem');

    render(
      <MemoryRouter initialEntries={['/auth/callback?code=one-time-code']}>
        <Routes>
          <Route path="/auth/callback" element={<CallbackPage />} />
          <Route path="/onboarding" element={<LocationProbe />} />
        </Routes>
      </MemoryRouter>,
    );

    await waitFor(() => expect(authApi.exchangeOAuth).toHaveBeenCalledWith('one-time-code'));
    expect(await screen.findByText(/"pathname":"\/onboarding"/)).toHaveTextContent('onboarding-token');
    expect(screen.getByText(/"pathname":"\/onboarding"/)).toHaveTextContent('"search":""');
    expect(storageSpy).not.toHaveBeenCalled();
  });
});
