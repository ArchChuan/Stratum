import { z } from 'zod';

export const tenantSummarySchema = z
  .object({
    id: z.union([z.string(), z.number()]).optional(),
    tenant_id: z.string().optional(),
    name: z.string(),
  })
  .passthrough();
export type TenantSummary = z.infer<typeof tenantSummarySchema>;

export const currentTenantSchema = z
  .object({
    id: z.union([z.string(), z.number()]),
    name: z.string().optional().default(''),
    role: z.string().optional(),
  })
  .passthrough();
export type CurrentTenant = z.infer<typeof currentTenantSchema>;

export const userSchema = z
  .object({
    sub: z.string().optional(),
    tenant_id: z.string().optional(),
    role: z.string().optional(),
    global_role: z.string().optional(),
    current_tenant: currentTenantSchema.nullable().optional(),
    avatar_url: z.string().optional().default(''),
    github_login: z.string().optional().default(''),
  })
  .passthrough();
export type User = z.infer<typeof userSchema>;

export const memberSchema = z
  .object({
    user_id: z.string(),
    github_login: z.string().optional().default(''),
    avatar_url: z.string().optional().default(''),
    role: z.string(),
    joined_at: z.string().optional(),
  })
  .passthrough();
export type Member = z.infer<typeof memberSchema>;

export const tenantSettingsSchema = z
  .object({
    tenant_id: z.string().optional(),
    tenant_name: z.string().optional().default(''),
    embed_model: z.string().optional().nullable(),
    llm_api_keys: z.record(z.string()).optional(),
  })
  .passthrough();
export type TenantSettings = z.infer<typeof tenantSettingsSchema>;

export const adminTenantSchema = z
  .object({
    id: z.union([z.string(), z.number()]),
    name: z.string(),
    slug: z.string().optional().default(''),
    status: z.string().optional().default(''),
    member_count: z.number().optional(),
    created_at: z.string().optional(),
  })
  .passthrough();
export type AdminTenant = z.infer<typeof adminTenantSchema>;
