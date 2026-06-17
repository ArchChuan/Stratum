import { describe, it, expect } from 'vitest';

import {
  tenantSummarySchema,
  userSchema,
  tenantSettingsSchema,
  adminTenantSchema,
} from '../auth';

describe('iam model schemas', () => {
  describe('tenantSummarySchema', () => {
    it('接受最小合法对象', () => {
      const parsed = tenantSummarySchema.parse({ name: 'foo' });
      expect(parsed.name).toBe('foo');
    });
    it('保留未声明字段（passthrough）', () => {
      const parsed = tenantSummarySchema.parse({ name: 'x', extra: 1 }) as { extra: number };
      expect(parsed.extra).toBe(1);
    });
    it('缺少 name 时报错', () => {
      expect(() => tenantSummarySchema.parse({})).toThrow();
    });
  });

  describe('userSchema', () => {
    it('全部字段缺省时使用默认值', () => {
      const u = userSchema.parse({});
      expect(u.avatar_url).toBe('');
      expect(u.github_login).toBe('');
    });
    it('current_tenant 可为 null', () => {
      const u = userSchema.parse({ current_tenant: null });
      expect(u.current_tenant).toBeNull();
    });
  });

  describe('tenantSettingsSchema', () => {
    it('llm_api_keys 必须是字符串映射', () => {
      const parsed = tenantSettingsSchema.parse({ llm_api_keys: { qwen: 'sk-1' } });
      expect(parsed.llm_api_keys?.qwen).toBe('sk-1');
    });
    it('embed_model 可为 null', () => {
      const parsed = tenantSettingsSchema.parse({ embed_model: null });
      expect(parsed.embed_model).toBeNull();
    });
  });

  describe('adminTenantSchema', () => {
    it('id 接受字符串或数字', () => {
      expect(adminTenantSchema.parse({ id: 'a', name: 'A' }).id).toBe('a');
      expect(adminTenantSchema.parse({ id: 1, name: 'A' }).id).toBe(1);
    });
  });
});
