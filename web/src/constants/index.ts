// Behavioral constants — no UI styling numbers here.

export const API_DEFAULT_TIMEOUT_MS = 10_000;
export const AGENT_EXEC_TIMEOUT_MS = 120_000;

export const DEFAULT_PAGE_SIZE = 20;
export const COMPACT_PAGE_SIZE = 10;
export const PAGE_SIZE_OPTIONS = ['10', '20', '50'];

export const MCP_DEFAULT_TIMEOUT_SEC = 30;
export const MCP_MAX_TIMEOUT_SEC = 300;
export const MCP_RETRY_INITIAL_DELAY_MS = 1000;
export const MCP_RETRY_MAX_DELAY_MS = 30000;
export const MCP_RETRY_MAX_RETRIES = 5;
export const MCP_RETRY_BACKOFF_FACTOR = 2.0;

export const SKILL_DEFAULT_TEMPERATURE = 0.7;
export const SKILL_DEFAULT_MAX_TOKENS = 2048;
export const SKILL_DEFAULT_TIMEOUT_SEC = 30;

export const MEMORY_SEARCH_LIMIT = 20;

interface ModelOption {
  value: string;
  label: string;
}

interface ModelOptionGroup {
  label: string;
  options: ModelOption[];
}

export const CHAT_MODEL_OPTIONS: ModelOptionGroup[] = [
  {
    label: '通义千问 (Qwen)',
    options: [
      { value: 'qwen-turbo', label: 'qwen-turbo（速度快，适合简单任务）' },
      { value: 'qwen-plus', label: 'qwen-plus（均衡）' },
      { value: 'qwen-max', label: 'qwen-max（最强，适合复杂推理）' },
      { value: 'qwen-long', label: 'qwen-long（超长上下文）' },
    ],
  },
  {
    label: '智谱 AI (Zhipu)',
    options: [
      { value: 'glm-4-flash', label: 'glm-4-flash（速度快，低成本）' },
      { value: 'glm-4-air', label: 'glm-4-air（均衡）' },
      { value: 'glm-4', label: 'glm-4（高精度）' },
      { value: 'glm-4v', label: 'glm-4v（视觉多模态）' },
    ],
  },
];

export const EMBEDDING_MODEL_OPTIONS: ModelOptionGroup[] = [
  {
    label: '通义千问 (Qwen)',
    options: [
      { value: 'text-embedding-v3', label: 'text-embedding-v3（推荐，1024/2048/3072维）' },
      { value: 'text-embedding-v2', label: 'text-embedding-v2（1536维）' },
    ],
  },
  {
    label: '智谱 AI (Zhipu)',
    options: [
      { value: 'embedding-3', label: 'embedding-3（2048维）' },
    ],
  },
];
