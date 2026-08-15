import type { EvidenceSummary } from './types';

export interface EvidenceRecord {
  ui: string[];
  http: string[];
  database: string[];
  httpRequests: Set<string>; // "METHOD pathname",原始形态,去重
}

export const emptyEvidence = (): EvidenceRecord => ({
  ui: [],
  http: [],
  database: [],
  httpRequests: new Set(),
});

export const summarizeEvidence = (record: EvidenceRecord): EvidenceSummary => ({
  ui: record.ui.length,
  http: record.http.length,
  database: record.database.length,
  reconciled: record.ui.length > 0 && record.http.length > 0 && record.database.length > 0,
});
