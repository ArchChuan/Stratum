// Behavioral constants — no UI styling numbers here.

export const API_DEFAULT_TIMEOUT_MS = 10_000;
export const AGENT_EXEC_TIMEOUT_MS = 120_000;

export const DEFAULT_PAGE_SIZE = 20;
export const COMPACT_PAGE_SIZE = 10;
export const PAGE_SIZE_OPTIONS = ['10', '20', '50'];

export const WORKFLOW_DEFAULT_PAGE_SIZE = 20;
export const WORKFLOW_VALIDATION_FOCUS_MS = 320;
export const WORKFLOW_NODE_WIDTH = 224;
export const WORKFLOW_NODE_HEIGHT = 88;

export const MCP_DEFAULT_TIMEOUT_SEC = 30;
export const MCP_MAX_TIMEOUT_SEC = 300;
export const MCP_RETRY_INITIAL_DELAY_MS = 1000;
export const MCP_RETRY_MAX_DELAY_MS = 30000;
export const MCP_RETRY_MAX_RETRIES = 5;
export const MCP_RETRY_BACKOFF_FACTOR = 2.0;

export const SKILL_DEFAULT_TEMPERATURE = 0.7;
export const SKILL_DEFAULT_MAX_TOKENS = 2048;
export const SKILL_DEFAULT_TIMEOUT_SEC = 30;
export const EVALUATION_JOB_POLL_INTERVAL_MS = 1000;
export const EVALUATION_JOB_MAX_WAIT_MS = 120000;

export const MEMORY_SEARCH_LIMIT = 20;

export const KNOWLEDGE_DEFAULT_CHUNK_SIZE = 512;
export const KNOWLEDGE_DEFAULT_CHUNK_OVERLAP = 64;
export const KNOWLEDGE_DEFAULT_TOP_K = 5;
export const KNOWLEDGE_MIN_CHUNK_SIZE = 64;
export const KNOWLEDGE_MAX_CHUNK_SIZE = 2048;
export const KNOWLEDGE_MIN_CHUNK_OVERLAP = 0;
export const KNOWLEDGE_MAX_CHUNK_OVERLAP = 512;
export const KNOWLEDGE_MIN_TOP_K = 1;
export const KNOWLEDGE_MAX_TOP_K = 20;

// Memory v2
export const MEMORY_SCOPE_OPTIONS = [
  { value: 'user', label: '用户级' },
  { value: 'agent', label: 'Agent 级' },
];

export const MEMORY_DIAGNOSTICS_REFRESH_INTERVAL_MS = 30000; // 30s
export const MEMORY_TOP_ENTITIES_LIMIT = 10;

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
      { value: 'qwen-max-latest', label: 'qwen-max-latest（最强，自动跟随最新版）' },
      { value: 'qwen-max', label: 'qwen-max（最强，适合复杂推理）' },
      { value: 'qwen-plus-latest', label: 'qwen-plus-latest（均衡，自动跟随最新版）' },
      { value: 'qwen-plus', label: 'qwen-plus（均衡）' },
      { value: 'qwen-turbo-latest', label: 'qwen-turbo-latest（快速，自动跟随最新版）' },
      { value: 'qwen-turbo', label: 'qwen-turbo（速度快，适合简单任务）' },
      { value: 'qwen-long', label: 'qwen-long（超长上下文）' },
    ],
  },
  {
    label: '智谱 AI (Zhipu)',
    options: [
      { value: 'glm-5.2', label: 'glm-5.2（旗舰，复杂推理）' },
      { value: 'glm-4.7-flashx', label: 'glm-4.7-flashx（快速，低成本）' },
      { value: 'glm-4.7-flash', label: 'glm-4.7-flash（快速，免费）' },
      { value: 'glm-4.5-flash', label: 'glm-4.5-flash（快速，免费）' },
      { value: 'glm-4-plus', label: 'glm-4-plus（高精度）' },
      { value: 'glm-4', label: 'glm-4（均衡）' },
      { value: 'glm-4-air', label: 'glm-4-air（轻量均衡）' },
      { value: 'glm-4-flash', label: 'glm-4-flash（速度快，低成本）' },
      { value: 'glm-4v', label: 'glm-4v（视觉多模态）' },
    ],
  },
];

export const CHUNKING_STRATEGY_OPTIONS = [
  { value: 'structure_recursive', label: '结构感知（推荐）— Markdown 标题分层 + 递归分块' },
  { value: 'recursive', label: '递归分块 — 按字符边界递归切分' },
  { value: 'semantic', label: '语义分块 — 按语义相似度切分（需嵌入模型）' },
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
