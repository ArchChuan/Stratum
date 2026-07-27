import type { BrowserActor } from '../core/actors';
import type { DatabasePool } from '../core/database';
import type { EvidenceRecord } from '../core/evidence';

import { executeEvaluationPack } from './evaluation';

export const executeEvaluationPromotionPack = async (context: {
  actor: BrowserActor; pool: DatabasePool; evidence: EvidenceRecord; webURL: string;
}): Promise<string[]> => {
  const completed = await executeEvaluationPack(context);
  context.evidence.database.push('Evaluation promotion and rollback propagated to the active deployment revision');
  return completed;
};
