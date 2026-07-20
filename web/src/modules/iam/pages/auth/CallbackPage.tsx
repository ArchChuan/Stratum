import { Spin, Alert } from 'antd';
import { useEffect, useRef, useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';

import { authApi } from '../../api/auth.api';
import { useAuth } from '../../components/AuthContext';

export const CallbackPage = () => {
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const { login } = useAuth();
  const [error, setError] = useState<string | null>(null);
  const handledRef = useRef(false);

  useEffect(() => {
    if (handledRef.current) return;
    handledRef.current = true;

    (async () => {
      try {
        const code = searchParams.get('code');
        if (!code) {
          setError('登录回调参数缺失，请重新登录');
          return;
        }
        navigate('/auth/callback', { replace: true });
        const exchange = await authApi.exchangeOAuth(code);

        if (exchange.kind === 'onboarding') {
          navigate('/onboarding', {
            replace: true,
            state: {
              onboardingToken: exchange.onboarding_token,
              githubLogin: exchange.github_login,
              avatarURL: exchange.avatar_url,
            },
          });
          return;
        }

        if (exchange.kind === 'login') {
          const me = await authApi.me(exchange.access_token);
          login(
            {
              sub: me.sub,
              tenant_id: me.tenant_id,
              role: me.role,
              global_role: me.global_role,
              system_role: me.system_role,
              current_tenant: me.tenant_id ? { id: me.tenant_id, name: '' } : null,
              avatar_url: me.avatar_url || '',
              github_login: me.github_login || '',
            },
            exchange.access_token,
          );
          navigate('/', { replace: true });
          return;
        }
      } catch (err: any) {
        setError(err?.response?.data?.message || '登录失败，请重试');
      }
    })();
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  if (error) {
    return (
      <div
        className="auth-page"
        style={{
          minHeight: '100vh',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
        }}
      >
        <Alert
          className="auth-card long-text"
          style={{ width: '100%', maxWidth: 440 }}
          type="error"
          message={error}
          description={<a href="/login">返回登录</a>}
        />
      </div>
    );
  }

  return (
    <div
      className="auth-page"
      style={{
        minHeight: '100vh',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
      }}
    >
      <Spin size="large" tip="正在完成登录..." />
    </div>
  );
};

export default CallbackPage;
