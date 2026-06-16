import React, { createContext, useState, useEffect, useRef } from 'react';
import api from '../services/api';
import { getTenantList, switchTenant as apiSwitchTenant } from '../services/api';

export const AuthContext = createContext(null);

// Module-level dedup: React 18 StrictMode fires useEffect twice in dev.
// Both calls would send /auth/refresh simultaneously; the second would fail
// because token rotation already invalidated the first refresh token.
// This promise ref ensures only one in-flight refresh exists at a time.
let _refreshPromise = null;

export const AuthProvider = ({ children }) => {
  const [user, setUser] = useState(null);
  const [accessToken, setAccessToken] = useState(null);
  const [tenants, setTenants] = useState([]);
  const [loading, setLoading] = useState(true);
  const tokenRef = useRef(null);

  const updateToken = (token) => {
    tokenRef.current = token;
    setAccessToken(token);
  };

  const buildUser = (meData, tenantName) => ({
    sub: meData.sub,
    tenant_id: meData.tenant_id,
    role: meData.role,
    global_role: meData.global_role,
    current_tenant: meData.tenant_id ? { id: meData.tenant_id, name: tenantName || meData.tenant_id, role: meData.role } : null,
    avatar_url: meData.avatar_url || '',
    github_login: meData.github_login || '',
  });

  const fetchTenantName = async (token) => {
    try {
      const res = await api.get('/tenant/settings', {
        headers: token ? { Authorization: `Bearer ${token}` } : {},
        _retry: true,
      });
      return res.data.tenant_name || '';
    } catch {
      return '';
    }
  };

  const fetchTenants = async (token) => {
    try {
      const res = await api.get('/tenant/list', {
        headers: token ? { Authorization: `Bearer ${token}` } : {},
        _retry: true,
      });
      return res.data.tenants || [];
    } catch {
      return [];
    }
  };

  useEffect(() => {
    const restoreSession = async () => {
      try {
        if (!_refreshPromise) {
          _refreshPromise = api.post('/auth/refresh').finally(() => { _refreshPromise = null; });
        }
        const refreshRes = await _refreshPromise;
        const token = refreshRes.data.access_token;
        updateToken(token);

        const [meRes, tenantName, tenantList] = await Promise.all([
          api.get('/auth/me', { headers: { Authorization: `Bearer ${token}` }, _retry: true }),
          fetchTenantName(token),
          fetchTenants(token),
        ]);
        setUser(buildUser(meRes.data, tenantName));
        setTenants(tenantList);
      } catch {
        setUser(null);
        updateToken(null);
        setTenants([]);
      } finally {
        setLoading(false);
      }
    };
    restoreSession();
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  const login = (userData, token) => {
    updateToken(token);
    // Eagerly set what we have, then enrich with tenant name + list.
    setUser(userData);
    Promise.all([fetchTenantName(token), fetchTenants(token)]).then(([tenantName, tenantList]) => {
      setUser(buildUser(userData, tenantName));
      setTenants(tenantList);
    });
  };

  const logout = async () => {
    try {
      await api.post('/auth/logout');
    } catch {
      // ignore, force local cleanup
    }
    setUser(null);
    updateToken(null);
    setTenants([]);
  };

  const switchTenant = async (tenantId) => {
    try {
      const res = await apiSwitchTenant(tenantId);
      const newToken = res.data.access_token;
      updateToken(newToken);

      const [meRes, tenantName, tenantList] = await Promise.all([
        api.get('/auth/me', { headers: { Authorization: `Bearer ${newToken}` }, _retry: true }),
        api.get('/tenant/settings', { headers: { Authorization: `Bearer ${newToken}` }, _retry: true })
          .then(r => r.data.tenant_name || '').catch(() => ''),
        fetchTenants(newToken),
      ]);
      setUser(buildUser(meRes.data, tenantName));
      setTenants(tenantList);
    } catch (err) {
      throw err;
    }
  };

  return (
    <AuthContext.Provider
      value={{ user, accessToken, tokenRef, loading, tenants, login, logout, switchTenant, setAccessToken: updateToken }}
    >
      {children}
    </AuthContext.Provider>
  );
};
