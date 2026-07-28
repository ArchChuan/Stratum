import type { EvidenceSummary } from './types';

export interface EvidenceRecord {
  ui: string[];
  http: string[];
  database: string[];
}

export const summarizeEvidence = (record: EvidenceRecord): EvidenceSummary => ({
  ui: record.ui.length,
  http: record.http.length,
  database: record.database.length,
  reconciled: record.ui.length > 0 && record.http.length > 0 && record.database.length > 0,
});
