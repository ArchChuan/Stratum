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
        const onboardingToken = searchParams.get('onboarding_token');
        if (onboardingToken) {
          sessionStorage.setItem('onboarding_token', onboardingToken);
          sessionStorage.setItem('github_login', searchParams.get('github_login') || '');
          sessionStorage.setItem('avatar_url', searchParams.get('avatar_url') || '');
          navigate('/onboarding', { replace: true });
          return;
        }

        const accessToken = searchParams.get('access_token');
        if (accessToken) {
          const me = await authApi.me(accessToken);
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
            accessToken,
          );
          navigate('/', { replace: true });
          return;
        }

        setError('登录回调参数缺失，请重新登录');
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
