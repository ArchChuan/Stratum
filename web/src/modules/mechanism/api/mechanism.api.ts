import { z } from 'zod';

import { profileSchema, type Profile, type UpsertProfileInput } from '../model/mechanism';

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
};
