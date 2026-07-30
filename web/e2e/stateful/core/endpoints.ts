const DEFAULT_E2E_MCP_PORT = 19091;
const MIN_E2E_PORT = 1024;
const MAX_E2E_PORT = 65535;

export const resolveE2EMCPPort = (raw: string | undefined): number => {
  if (raw === undefined) return DEFAULT_E2E_MCP_PORT;
  if (!/^[0-9]+$/.test(raw)) throw new Error('STATEFUL_E2E_MCP_PORT must be between 1024 and 65535');
  const port = Number(raw);
  if (port < MIN_E2E_PORT || port > MAX_E2E_PORT) {
    throw new Error('STATEFUL_E2E_MCP_PORT must be between 1024 and 65535');
  }
  return port;
};

export const E2E_MCP_BASE_URL = `http://127.0.0.1:${resolveE2EMCPPort(process.env.STATEFUL_E2E_MCP_PORT)}`;
