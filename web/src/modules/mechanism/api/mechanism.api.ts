import { z } from 'zod';

import {
  matrixReportSchema,
  profileSchema,
  runMatrixResultSchema,
  type MatrixReport,
  type Profile,
  type RunMatrixResult,
  type UpsertProfileInput,
} from '../model/mechanism';

import api from '@/services/client';

export const mechanismApi = {
  /** GET /mechanism/profiles — 全部模型族档案。 */
  list: async (): Promise<Profile[]> => {
    const res = await api.get('/mechanism/profiles');
    const parsed = z
      .object({ profiles: z.array(profileSchema) })
      .passthrough()
      .parse(res.data ?? {});
    return parsed.profiles;
  },

  /** GET /mechanism/profiles/:familyKey — 单档案详情。 */
  get: async (familyKey: string): Promise<Profile> => {
    const res = await api.get(`/mechanism/profiles/${encodeURIComponent(familyKey)}`);
    return profileSchema.parse(res.data);
  },

  /** PUT /mechanism/profiles — 建档/覆盖（族键冲突升级版本）。 */
  upsert: async (input: UpsertProfileInput): Promise<Profile> => {
    const res = await api.put('/mechanism/profiles', input);
    return profileSchema.parse(res.data);
  },

  /** GET /mechanism/matrix — 评测矩阵工作台快照（基准集 × 档案单元格 × 前沿）。 */
  matrixReport: async (): Promise<MatrixReport> => {
    const res = await api.get('/mechanism/matrix');
    return matrixReportSchema.parse(res.data ?? {});
  },

  /** POST /mechanism/matrix/runs — 触发全部档案 × 基准集矩阵评测（异步）。 */
  runMatrix: async (): Promise<RunMatrixResult> => {
    const res = await api.post('/mechanism/matrix/runs');
    return runMatrixResultSchema.parse(res.data ?? {});
  },

  /** POST /mechanism/matrix/adopt — 采纳档案（draft → active 发布）。 */
  adopt: async (familyKey: string): Promise<Profile> => {
    const res = await api.post('/mechanism/matrix/adopt', { family_key: familyKey });
    return profileSchema.parse(res.data);
  },
};
