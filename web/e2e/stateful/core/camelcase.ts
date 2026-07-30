/**
 * camelCase JSON 守卫——可复用的 Playwright 断言工具。
 *
 * 用法：
 *   import { assertCamelCase, assertNoPascalCase } from '../core/camelcase';
 *
 *   const body = await response.json();
 *   assertCamelCase(body, ['id', 'name', 'displayName', 'createdAt']);
 *   assertNoPascalCase(body, ['DisplayName', 'CreatedAt']);
 *
 * 原理：
 *   Go struct 缺少 json tag 时 JSON 字段用 PascalCase（如 "DisplayName"），
 *   前端用 camelCase（如 "displayName"）取值 → undefined → 静默失效。
 *   Contract golden 在 build time、E2E pack 在 runtime 双重防御。
 */

/**
 * 断言 JSON 响应中包含所有指定的 camelCase 键。
 * 支持嵌套对象——递归搜索整个 JSON 树。
 */
export function assertCamelCaseKeys(
  body: unknown,
  requiredKeys: string[],
  label?: string,
): void {
  const raw = typeof body === 'string' ? body : JSON.stringify(body);
  const prefix = label ? `[${label}] ` : '';
  for (const key of requiredKeys) {
    if (!raw.includes(`"${key}"`)) {
      throw new Error(`${prefix}缺少必需的 camelCase 键: "${key}"`);
    }
  }
}

/**
 * 断言 JSON 响应中不包含任何 PascalCase 键。
 */
export function assertNoPascalCase(
  body: unknown,
  bannedKeys: string[],
  label?: string,
): void {
  const raw = typeof body === 'string' ? body : JSON.stringify(body);
  const prefix = label ? `[${label}] ` : '';
  for (const key of bannedKeys) {
    if (raw.includes(`"${key}"`)) {
      throw new Error(`${prefix}发现禁止的 PascalCase 键: "${key}"，应使用 camelCase`);
    }
  }
}

/**
 * LLM Provider 响应必需的 camelCase 键。
 */
export const PROVIDER_CAMEL_KEYS = [
  'baseUrl', 'createdAt', 'updatedAt', 'defaultModel',
];

/**
 * LLM Provider 响应禁止的 PascalCase 键。
 */
export const PROVIDER_PASCAL_BANNED = [
  'BaseURL', 'CreatedAt', 'UpdatedAt', 'DefaultModel',
];

/**
 * LLM Model 响应必需的 camelCase 键。
 */
export const MODEL_CAMEL_KEYS = [
  'displayName', 'providerId', 'providerManaged',
  'contextWindow', 'maxTokens', 'inputPrice', 'outputPrice',
  'createdAt', 'updatedAt',
];

/**
 * LLM Model 响应禁止的 PascalCase 键。
 */
export const MODEL_PASCAL_BANNED = [
  'DisplayName', 'ProviderID', 'ProviderManaged',
  'ContextWindow', 'MaxTokens', 'InputPrice', 'OutputPrice',
  'CreatedAt', 'UpdatedAt',
];
