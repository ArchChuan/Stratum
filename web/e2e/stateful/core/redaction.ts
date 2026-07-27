const SENSITIVE_KEY = /authorization|cookie|password|passphrase|private.?key|api.?key|access.?token|refresh.?token|secret/i;

export const redactSensitive = (value: unknown): unknown => {
  if (Array.isArray(value)) return value.map(redactSensitive);
  if (value === null || typeof value !== 'object') return value;

  return Object.fromEntries(Object.entries(value).map(([key, entry]) => [
    key,
    SENSITIVE_KEY.test(key) ? '[REDACTED]' : redactSensitive(entry),
  ]));
};
