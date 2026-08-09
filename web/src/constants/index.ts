// Behavioral constants — no UI styling numbers here.

export const API_DEFAULT_TIMEOUT_MS = 10_000;
export const AGENT_EXEC_TIMEOUT_MS = 120_000;
export const AGENT_DEFAULT_MAX_ITERATIONS = 10;
export const AGENT_MIN_MAX_ITERATIONS = 1;
export const AGENT_MAX_MAX_ITERATIONS = 90;

export const DEFAULT_PAGE_SIZE = 20;
export const COMPACT_PAGE_SIZE = 10;
export const PAGE_SIZE_OPTIONS = ['10', '20', '50'];

export const WORKFLOW_DEFAULT_PAGE_SIZE = 20;
export const WORKFLOW_VALIDATION_FOCUS_MS = 320;
export const WORKFLOW_NODE_WIDTH = 224;
export const WORKFLOW_NODE_HEIGHT = 88;
export const WORKFLOW_STREAM_RECONNECT_BASE_MS = 1000;
export const WORKFLOW_STREAM_RECONNECT_MAX_MS = 10000;
export const WORKFLOW_OUTPUT_MAX_CHARS = 100000;

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

export const LLM_DEFAULT_PAGE_SIZE = 20;

export const KNOWLEDGE_DEFAULT_CHUNK_SIZE = 512;
export const KNOWLEDGE_DEFAULT_CHUNK_OVERLAP = 64;
export const KNOWLEDGE_DEFAULT_TOP_K = 5;
export const KNOWLEDGE_MIN_CHUNK_SIZE = 64;
export const KNOWLEDGE_MAX_CHUNK_SIZE = 2048;
export const KNOWLEDGE_MIN_CHUNK_OVERLAP = 0;
export const KNOWLEDGE_MAX_CHUNK_OVERLAP = 512;
export const KNOWLEDGE_MIN_TOP_K = 1;
export const KNOWLEDGE_MAX_TOP_K = 20;
export const KNOWLEDGE_MAX_UPLOAD_SIZE_BYTES = 10 * 1024 * 1024; // 10MB，与 UI 提示一致（后端上限 100MB）
export const AVATAR_MAX_UPLOAD_SIZE_BYTES = 2 * 1024 * 1024; // 2MB，与 UI 提示一致

// Memory v2
export const MEMORY_SCOPE_OPTIONS = [
  { value: 'user', label: '用户级' },
  { value: 'agent', label: 'Agent 级' },
];

export const MEMORY_DIAGNOSTICS_REFRESH_INTERVAL_MS = 30000; // 30s
export const MEMORY_TOP_ENTITIES_LIMIT = 10;

export const CHUNKING_STRATEGY_OPTIONS = [
  { value: 'structure_recursive', label: '结构感知（推荐）— Markdown 标题分层 + 递归分块' },
  { value: 'recursive', label: '递归分块 — 按字符边界递归切分' },
  { value: 'semantic', label: '语义分块 — 按语义相似度切分（需嵌入模型）' },
];
