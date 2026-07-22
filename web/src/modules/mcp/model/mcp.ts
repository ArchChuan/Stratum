import { z } from 'zod';

export const mcpToolSchema = z
  .object({
    name: z.string(),
    description: z.string().optional().default(''),
  })
  .passthrough();
export type MCPTool = z.infer<typeof mcpToolSchema>;
export interface MCPToolOption { id: string; label: string; serverId: string; toolName: string }
export type MCPToolRiskLevel = 'read' | 'write_reversible' | 'destructive' | 'unclassified';
export interface MCPToolPolicy { serverId: string; toolName: string; riskLevel: MCPToolRiskLevel }

export const mcpResourceSchema = z
  .object({
    uri: z.string(),
    name: z.string().optional().default(''),
    mimeType: z.string().optional().default(''),
  })
  .passthrough();
export type MCPResource = z.infer<typeof mcpResourceSchema>;

export const mcpServerSchema = z
  .object({
    id: z.string(),
    name: z.string(),
    transport: z.string(),
    status: z.string().optional().default('disconnected'),
    version: z.string().optional().default(''),
    tools: z.array(mcpToolSchema).optional(),
  })
  .passthrough();
export type MCPServer = z.infer<typeof mcpServerSchema>;

export interface MCPRetryConfig {
  enabled: boolean;
  max_retries: number;
  initial_delay_ms: number;
  max_delay_ms: number;
  backoff_factor: number;
}

export interface MCPAuthConfig {
  type: string;
  token?: string;
  api_key_header?: string;
  api_key_value?: string;
  oauth2_client_id?: string;
  oauth2_client_secret?: string;
  oauth2_token_url?: string;
  oauth2_scopes?: string[];
}

export interface MCPAuthConfigResponse {
  type: string;
  api_key_header?: string;
  oauth2_client_id?: string;
  oauth2_token_url?: string;
  oauth2_scopes?: string[];
  credential_configured: boolean;
}

export interface MCPServerConfig {
  id: string;
  name: string;
  version: string;
  transport: string;
  timeout: number;
  command?: string;
  args?: string[];
  env?: Record<string, string>;
  url?: string;
  headers?: Record<string, string>;
  auth?: MCPAuthConfig;
  retry?: MCPRetryConfig;
}

export interface MCPServerConfigResponse extends Omit<MCPServerConfig, 'auth'> {
  auth?: MCPAuthConfigResponse;
}
