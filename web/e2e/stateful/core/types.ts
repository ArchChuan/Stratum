export interface EvidenceSummary {
  ui: number;
  http: number;
  database: number;
  reconciled: boolean;
}

export interface ActionResult<M> {
  next: M;
  evidence: EvidenceSummary;
}

export interface StatefulAction<M, C> {
  id: string;
  enabled(model: Readonly<M>): boolean;
  run(context: C, model: Readonly<M>): Promise<ActionResult<M>>;
}

export interface RandomSource {
  nextInt(maxExclusive: number): number;
}
