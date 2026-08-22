import { z } from 'zod';

import {
  parameterDefinitionSchema,
  type ParameterDefinition,
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
};
