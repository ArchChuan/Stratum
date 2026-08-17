import { z } from 'zod';

import {
  parameterDefinitionSchema,
  type ParameterDefinition,
  type PlatformValues,
  promptDefaultsSchema,
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

  /** PUT /admin/parameters — merge 写平台值;仅平台 scope key 可写。 */
  update: (values: PlatformValues): Promise<PlatformValues> =>
    api.put('/admin/parameters', values),

  /**
   * GET /parameters/prompt-defaults — 默认提示词模板白名单(registry key → 全文)。
   * 平台页走 admin 通道,agent 编辑页走租户 admin 通道(同一数据,双组注册)。
   */
  promptDefaults: async (): Promise<Record<string, string>> => {
    const res = await api.get('/parameters/prompt-defaults');
    return promptDefaultsSchema.parse(res.data ?? {});
  },
};
