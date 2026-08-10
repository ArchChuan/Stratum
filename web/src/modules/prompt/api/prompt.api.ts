import { z } from 'zod';

import {
  promptBindingSchema,
  promptSummarySchema,
  promptTemplateSchema,
  type PromptBinding,
  type PromptSummary,
  type PromptTemplate,
} from '../model/prompt';

import api from '@/services/client';

export interface ListPromptsInput {
  page?: number;
  pageSize?: number;
}

export interface UpsertBindingInput {
  key: string;
  scope: string;
  stable_version_id: string;
  canary_version_id?: string;
  traffic_percent?: number;
}

export const promptApi = {
  /** POST /prompts — 创建模板（key 创建后不可变，内容寻址版本机制）。 */
  create: (data: { key: string; content: string }) => api.post('/prompts', data),

  /** GET /prompts — 每个 key 的最新版本摘要列表（admin）。 */
  listPrompts: async ({ page = 1, pageSize = 20 }: ListPromptsInput = {}): Promise<{ prompts: PromptSummary[]; total: number }> => {
    const res = await api.get('/prompts', { params: { page, page_size: pageSize } });
    return {
      prompts: z.array(promptSummarySchema).parse(res.data?.prompts ?? []),
      total: (res.data?.total ?? 0) as number,
    };
  },

  /** GET /prompts/:key/versions — 指定模板的版本历史（新版在前）。 */
  listVersions: async (key: string): Promise<PromptTemplate[]> => {
    const res = await api.get(`/prompts/${encodeURIComponent(key)}/versions`);
    return z.array(promptTemplateSchema).parse(res.data?.versions ?? []);
  },

  /** POST /prompts/:key/versions/:version/publish — 发布指定版本。 */
  publishVersion: (key: string, version: number) =>
    api.post(`/prompts/${encodeURIComponent(key)}/versions/${version}/publish`),

  /** GET /prompts/bindings — 全部 A/B 绑定（admin）。 */
  listBindings: async (): Promise<PromptBinding[]> => {
    const res = await api.get('/prompts/bindings');
    return z.array(promptBindingSchema).parse(res.data?.bindings ?? []);
  },

  /** PUT /prompts/bindings — 新建或更新 A/B 绑定。 */
  upsertBinding: (data: UpsertBindingInput) => api.put('/prompts/bindings', data),

  /** DELETE /prompts/bindings/:key/:scope — 清除 A/B 绑定。 */
  deleteBinding: (key: string, scope: string) =>
    api.delete(`/prompts/bindings/${encodeURIComponent(key)}/${encodeURIComponent(scope)}`),
};
