import type { Page } from '@playwright/test';

import type { DatabasePool } from '../core/database';
import type { EvidenceRecord } from '../core/evidence';

export interface PackContext {
  page: Page;
  evidence: EvidenceRecord;
  pool: DatabasePool;
  tenantID: string;
  webURL: string;
  fixtureURL: string;
}

export interface BrowserAction {
  id: string;
  execute(context: PackContext): Promise<void>;
}

export const actionIDs = (actions: BrowserAction[]): string[] => actions.map(({ id }) => id);
