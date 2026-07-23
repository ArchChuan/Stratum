import { expect, test } from './fixtures';

const resourceKinds = ['skill', 'agent', 'mcp', 'knowledge'] as const;

for (const kind of resourceKinds) {
  test(`${kind}: published baseline through explicit decision is visible and tenant isolated`, async ({
    adminApi, memberApi, manifest,
  }) => {
    const resource = manifest.resources[kind];
    const resources = await memberApi.get(`/evaluations/resources?resource_kind=${kind}&resource_id=${resource.id}`);
    expect(resources.status()).toBe(200);
    expect((await resources.json()).items).toEqual(expect.arrayContaining([
      expect.objectContaining({ resource_id: resource.id, stable_revision_id: resource.baselineRevision }),
    ]));

    const runs = await memberApi.get(`/evaluations/runs?resource_kind=${kind}&resource_id=${resource.id}`);
    expect(runs.status()).toBe(200);
    expect((await runs.json()).items).toEqual(expect.arrayContaining([
      expect.objectContaining({ revision_id: resource.baselineRevision, status: 'succeeded', passed: true }),
      expect.objectContaining({ revision_id: resource.candidateRevision, status: 'succeeded', passed: true }),
    ]));

    const experiments = await memberApi.get(`/evaluations/experiments?resource_kind=${kind}&resource_id=${resource.id}`);
    expect(experiments.status()).toBe(200);
    const experiment = (await experiments.json()).items.find((item: { id: string }) => item.id === resource.experimentId);
    expect(experiment).toMatchObject({ recommendation: resource.recommendation });
    expect(experiment.promotion_evidence).toBeDefined();

    const crossTenant = await adminApi.get(
      `/evaluations/resources/${kind}/${manifest.foreignResourceId}/timeline`,
    );
    expect(crossTenant.status()).toBe(404);

    const timeline = await memberApi.get(`/evaluations/resources/${kind}/${resource.id}/timeline`);
    expect(timeline.status()).toBe(200);
    expect((await timeline.json()).items).toEqual(expect.arrayContaining([
      expect.objectContaining({ kind: 'decision', summary: resource.decision }),
      expect.objectContaining({ kind: 'candidate' }),
      expect.objectContaining({ kind: 'run' }),
    ]));
  });
}

test.describe('responsive center', () => {
  for (const viewport of [
    { name: 'desktop', width: 1440, height: 900 },
    { name: 'mobile', width: 390, height: 844 },
  ]) {
    test(`${viewport.name} navigation exposes evidence without auto-promotion`, async ({ authenticatedPage, manifest }) => {
      await authenticatedPage.setViewportSize(viewport);
      await authenticatedPage.goto('/evaluations');
      await expect(authenticatedPage.getByRole('heading', { name: '评测与进化中心' })).toBeVisible();
      await expect(authenticatedPage.getByRole('cell', { name: manifest.resources.skill.id }).first()).toBeVisible();
      await authenticatedPage.getByRole('tab', { name: /金丝雀实验/ }).click();
      const skillExperiment = authenticatedPage.getByRole('row', {
        name: new RegExp(manifest.resources.skill.experimentId),
      });
      await skillExperiment.getByRole('button', { name: '详情' }).click();
      await expect(authenticatedPage.getByRole('heading', { name: '系统建议' })).toBeVisible();
      await expect(authenticatedPage.getByRole('button', { name: /晋\s*级/ })).toBeVisible();
      await authenticatedPage.reload();
      await expect(authenticatedPage.getByRole('cell', { name: manifest.resources.skill.id }).first()).toBeVisible();
    });
  }
});
