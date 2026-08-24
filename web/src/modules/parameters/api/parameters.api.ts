import { z } from 'zod';

import {
  parameterDefinitionSchema,
  type ParameterDefinition,
  type PlatformConfigVersion,
  type PlatformValues,
} from '../model/parameters';

import api from '@/services/client';

export const parametersApi = {
  /** GET /admin/parameters/schema — 全部参数定义(schema 驱动前端渲染)。 */
  schema: async (): Promise<ParameterDefinition[]> => {
    const res = await api.get('/admin/parameters/schema');
    return z.array(parameterDefinitionSchema).parse(res.data ?? []);
  },

  /** GET /admin/parameters — 当前生效的平台层值(缺失 key = unset)。 */
  list: async (): Promise<PlatformValues> => {
    const res = await api.get('/admin/parameters');
    return (res.data ?? {}) as PlatformValues;
  },

  /** PUT /admin/parameters — merge 写平台值；仅平台 scope 键（资源级键后端拒绝）。 */
  update: (values: PlatformValues): Promise<PlatformValues> =>
    api.put('/admin/parameters', values),

  /**
   * GET /admin/parameters/versions/:groupKey — 分组版本历史（newest first），
   * 每行含不可变 snapshot，作为平台配置变更的审计视图。
   */
  versions: async (groupKey: string): Promise<PlatformConfigVersion[]> => {
    const res = await api.get(`/admin/parameters/versions/${groupKey}`);
    return (res.data ?? []) as PlatformConfigVersion[];
  },

  /**
   * POST /admin/parameters/versions/:groupKey — 创建草稿版本（draft 是唯一可编辑
   * 状态）。snapshot 携带本组待变更键，message 为操作者备注。
   */
  createDraft: (
    groupKey: string,
    snapshot: PlatformValues,
    message: string,
  ): Promise<PlatformConfigVersion> =>
    api.post(`/admin/parameters/versions/${groupKey}`, { snapshot, message }),

  /**
   * POST /admin/parameters/versions/:groupKey/:versionID/publish — 发布草稿，
   * production/latest 标签移到该版本；返回发布后生效值。
   */
  publish: (groupKey: string, versionId: number): Promise<PlatformValues> =>
    api.post(`/admin/parameters/versions/${groupKey}/${versionId}/publish`),

  /**
   * POST /admin/parameters/versions/:groupKey/:versionID/rollback — 把
   * production/latest 标签指回历史已发布版本（不产生新版本）；返回回滚后生效值。
   */
  rollback: (groupKey: string, versionId: number): Promise<PlatformValues> =>
    api.post(`/admin/parameters/versions/${groupKey}/${versionId}/rollback`),
};
