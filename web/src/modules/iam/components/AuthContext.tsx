import {
  createContext,
  useContext,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type MutableRefObject,
  type ReactNode,
} from 'react';

import { authApi } from '../api/auth.api';
import { tenantApi } from '../api/tenant.api';
import type { TenantSummary, User } from '../model/auth';

import api, { setupApiInterceptors, markAuthReady, resetAuthReady } from '@/services/client';

interface AuthContextValue {
  user: User | null;
  accessToken: string | null;
  tokenRef: MutableRefObject<string | null>;
  loading: boolean;
  tenants: TenantSummary[];
  login: (userData: User, token: string) => void;
  logout: () => Promise<void>;
  switchTenant: (tenantId: string) => Promise<void>;
  setAccessToken: (token: string | null) => void;
}

const AuthContext = createContext<AuthContextValue | null>(null);

export const useAuth = (): AuthContextValue => {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error('useAuth must be used inside <AuthProvider>');
  return ctx;
};

let refreshPromise: Promise<{ access_token: string }> | null = null;

const buildUser = (meData: User, tenantName: string): User => ({
  ...meData,
  current_tenant: meData.tenant_id
    ? { id: meData.tenant_id, name: tenantName || meData.tenant_id, role: meData.role }
    : null,
});

const fetchTenantName = async (token?: string): Promise<string> => {
  try {
    const settings = await tenantApi.settings(token);
    return settings.tenant_name || '';
  } catch {
    return '';
  }
};

const fetchTenants = async (token?: string): Promise<TenantSummary[]> => {
  try {
    return await tenantApi.listMine(token);
  } catch {
    return [];
  }
};

export const AuthProvider = ({ children }: { children: ReactNode }) => {
  const [user, setUser] = useState<User | null>(null);
  const [accessToken, setAccessToken] = useState<string | null>(null);
  const [tenants, setTenants] = useState<TenantSummary[]>([]);
  const [loading, setLoading] = useState(true);
  const tokenRef = useRef<string | null>(null);

  const updateToken = (token: string | null) => {
    tokenRef.current = token;
    setAccessToken(token);
  };

  // Wire interceptors before any useEffect fires so _tokenRef is always valid.
  useLayoutEffect(() => {
    setupApiInterceptors(tokenRef, async () => {
      try { await authApi.logout(); } catch { /* ignore */ }
      setUser(null);
      updateToken(null);
      setTenants([]);
      resetAuthReady();
    });
  }, []);

  useEffect(() => {
    let cancelled = false;
    const restoreSession = async () => {
      try {
        if (!refreshPromise) {
          refreshPromise = api
            .post<{ access_token: string }>('/auth/refresh')
            .then((r) => r.data)
            .finally(() => {
              refreshPromise = null;
            });
        }
        const { access_token: token } = await refreshPromise;
        if (cancelled) return;
        updateToken(token);
        markAuthReady();
        const [me, tenantName, tenantList] = await Promise.all([
          authApi.me(token),
          fetchTenantName(token),
          fetchTenants(token),
        ]);
        if (cancelled) return;
        // Re-assert token in case a concurrent restoreSession cleared it.
        updateToken(token);
        setUser(buildUser(me, tenantName));
        setTenants(tenantList);
      } catch {
        if (cancelled) return;
        setUser(null);
        updateToken(null);
        setTenants([]);
        markAuthReady();
      } finally {
        if (!cancelled) setLoading(false);
      }
    };
    restoreSession();
    return () => { cancelled = true; };
  }, []);

  const login = (userData: User, token: string) => {
    updateToken(token);
    markAuthReady();
    setUser(userData);
    Promise.all([fetchTenantName(token), fetchTenants(token)]).then(
      ([tenantName, tenantList]) => {
        setUser(buildUser(userData, tenantName));
        setTenants(tenantList);
      },
    );
  };

  const logout = async () => {
    try {
      await authApi.logout();
    } catch {
      // ignore
    }
    setUser(null);
    updateToken(null);
    setTenants([]);
    resetAuthReady();
  };

  const switchTenant = async (tenantId: string) => {
    const { access_token: newToken } = await authApi.switchTenant(tenantId);
    updateToken(newToken);
    markAuthReady();
    const [me, tenantName, tenantList] = await Promise.all([
      authApi.me(newToken),
      fetchTenantName(newToken),
      fetchTenants(newToken),
    ]);
    setUser(buildUser(me, tenantName));
    setTenants(tenantList);
  };

  const value: AuthContextValue = {
    user,
    accessToken,
    tokenRef,
    loading,
    tenants,
    login,
    logout,
    switchTenant,
    setAccessToken: updateToken,
  };

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
};
