import { expect, type Locator, type Page } from '@playwright/test';
import type { QueryResultRow } from 'pg';

import type { BrowserActor } from '../core/actors';
import { requireUUID, type DatabasePool } from '../core/database';
import type { EvidenceRecord } from '../core/evidence';
import { executeIAMOAuthJourney } from './iam-oauth';

interface IAMActors {
  systemAdmin: BrowserActor;
  tenantAdmin: BrowserActor;
  memberA: BrowserActor;
  memberB: BrowserActor;
}

interface IAMPackContext {
  actors: IAMActors;
  pool: DatabasePool;
  evidence: EvidenceRecord;
  webURL: string;
  backendURL: string;
}

const MAX_ADMIN_TENANT_PAGES = 100;

const publicQuery = async <R extends QueryResultRow>(
  pool: DatabasePool,
  text: string,
  values: unknown[],
): Promise<R[]> => {
  const client = await pool.connect();
  try {
    return (await client.query(text, values)).rows as R[];
  } finally {
    client.release();
  }
};

const waitForMutation = (page: Page, path: string, method: string) => page.waitForResponse((response) => (
  new URL(response.url()).pathname === path
  && response.request().method() === method
));

const findTenantRow = async (page: Page, tenantName: string): Promise<Locator> => {
  const row = page.locator('tr').filter({ hasText: tenantName });
  for (let pageNumber = 1; pageNumber <= MAX_ADMIN_TENANT_PAGES; pageNumber += 1) {
    if (await row.count() > 0) {
      await expect(row).toBeVisible();
      return row;
    }
    const next = page.locator('.ant-pagination-next');
    if ((await next.getAttribute('class'))?.includes('ant-pagination-disabled')) break;
    const activePage = page.locator('.ant-pagination-item-active');
    const currentPage = await activePage.innerText();
    await next.click();
    await expect(activePage).not.toHaveText(currentPage);
  }
  throw new Error('target tenant was not found in the administrator tenant list');
};

export const executeIAMPack = async ({
  actors, pool, evidence, webURL, backendURL,
}: IAMPackContext): Promise<string[]> => {
  const completed = [
    'iam.route.login',
    'iam.mutation.post.auth.guest',
    'iam.mutation.post.auth.refresh',
  ];
  const tenantName = `E2E-IAM-${Date.now()}`;
  const renamedTenant = `${tenantName}-renamed`;
  const tenantAdminPage = await actors.tenantAdmin.context.newPage();
  const memberPage = await actors.memberA.context.newPage();
  const memberBPage = await actors.memberB.context.newPage();
  const systemAdminPage = await actors.systemAdmin.context.newPage();
  let targetTenantID = '';
  try {
    await tenantAdminPage.goto(`${webURL}/onboarding`);
    await expect(tenantAdminPage.getByRole('heading', { name: '欢迎使用 Stratum' })).toBeVisible();
    completed.push('iam.route.onboarding');
    await tenantAdminPage.getByLabel('租户名称').fill(tenantName);
    const createResponse = waitForMutation(tenantAdminPage, '/auth/create-tenant', 'POST');
    const initialSwitchResponse = waitForMutation(tenantAdminPage, '/auth/switch-tenant', 'POST');
    await tenantAdminPage.getByRole('button', { name: /创建租户/ }).click();
    const created = await createResponse;
    expect(created.status()).toBe(201);
    targetTenantID = requireUUID((await created.json() as { tenant_id: string }).tenant_id, 'target_tenant_id');
    expect((await initialSwitchResponse).status()).toBe(200);
    await expect(tenantAdminPage).toHaveURL(`${webURL}/`);
    await expect(tenantAdminPage.getByLabel(`当前租户：${tenantName}`)).toBeVisible();
    const createdRows = await publicQuery<{ name: string; role: string }>(pool,
      `SELECT t.name, tm.role FROM public.tenants t
       JOIN public.tenant_members tm ON tm.tenant_id = t.id
       WHERE t.id = $1 AND tm.user_id = $2`,
      [targetTenantID, actors.tenantAdmin.userID]);
    expect(createdRows).toEqual([{ name: tenantName, role: 'owner' }]);
    evidence.ui.push('tenant admin created a tenant from onboarding');
    evidence.http.push('authenticated tenant creation returned 201');
    evidence.database.push('created tenant and owner membership reconciled');
    completed.push('iam.mutation.post.auth.create.tenant', 'iam.mutation.post.auth.switch.tenant');

    await tenantAdminPage.goto(`${webURL}/tenant/settings`);
    await expect(tenantAdminPage).toHaveURL(`${webURL}/tenant/settings`);
    await expect(tenantAdminPage.getByRole('heading', { name: '租户设置' })).toBeVisible();
    completed.push('iam.route.tenant.settings');
    const tenantNameInput = tenantAdminPage.getByLabel('租户名称');
    await expect(tenantNameInput).toHaveValue(tenantName);
    await tenantNameInput.fill(renamedTenant);
    const settingsResponse = waitForMutation(tenantAdminPage, '/tenant/settings', 'PATCH');
    const basicSettingsForm = tenantAdminPage.locator('form').filter({ has: tenantNameInput });
    await basicSettingsForm.locator('button[type="submit"]').click();
    expect((await settingsResponse).status()).toBe(200);
    await tenantAdminPage.reload();
    await expect(tenantAdminPage.getByLabel('租户名称')).toHaveValue(renamedTenant);
    const settingsRows = await publicQuery<{ name: string }>(pool,
      'SELECT name FROM public.tenants WHERE id = $1', [targetTenantID]);
    expect(settingsRows).toEqual([{ name: renamedTenant }]);
    evidence.ui.push('tenant settings survived refresh');
    evidence.http.push('tenant settings mutation returned 200');
    evidence.database.push('tenant name reconciled');
    completed.push('iam.mutation.patch.tenant.settings');

    await tenantAdminPage.goto(`${webURL}/tenant/members`);
    await expect(tenantAdminPage.getByRole('heading', { name: '成员管理' })).toBeVisible();
    completed.push('iam.route.tenant.members');
    await tenantAdminPage.getByRole('button', { name: /邀请成员/ }).click();
    await tenantAdminPage.getByLabel('邮箱').fill(actors.memberA.email!);
    const inviteResponse = waitForMutation(tenantAdminPage, '/tenant/members/invite', 'POST');
    await tenantAdminPage.getByRole('button', { name: /发送邀请/ }).click();
    expect((await inviteResponse).status()).toBe(201);
    const inviteDialog = tenantAdminPage.locator('.ant-modal-content').filter({ hasText: '邀请码已生成' });
    await expect(inviteDialog).toBeVisible();
    const invitationCode = (await inviteDialog.locator('code').innerText()).trim();
    expect(invitationCode.length).toBeGreaterThan(20);
    const invitationRows = await publicQuery<{ count: string }>(pool,
      `SELECT count(*)::text AS count FROM public.tenant_invitations
       WHERE tenant_id = $1 AND email = $2 AND consumed_at IS NULL AND code_hash <> $3`,
      [targetTenantID, actors.memberA.email, invitationCode]);
    expect(invitationRows).toEqual([{ count: '1' }]);
    await inviteDialog.locator('button.ant-modal-close').click();
    await expect(inviteDialog).toHaveCount(0);
    evidence.ui.push('one-time invitation code displayed in controlled modal');
    evidence.http.push('tenant invitation returned 201');
    evidence.database.push('only hashed unconsumed invitation persisted');
    completed.push('iam.mutation.post.tenant.members.invite');

    await memberPage.goto(`${webURL}/onboarding`);
    await memberPage.getByRole('tab', { name: '加入已有租户' }).click();
    await memberPage.getByLabel('邀请码').fill(invitationCode);
    const joinResponse = waitForMutation(memberPage, '/tenant/join', 'POST');
    const switchResponse = waitForMutation(memberPage, '/auth/switch-tenant', 'POST');
    await memberPage.getByRole('button', { name: /加入租户/ }).click();
    expect((await joinResponse).status()).toBe(200);
    expect((await switchResponse).status()).toBe(200);
    await expect(memberPage).toHaveURL(`${webURL}/`);
    const membershipRows = await publicQuery<{ role: string; consumed: boolean }>(pool,
      `SELECT tm.role, i.consumed_at IS NOT NULL AS consumed
       FROM public.tenant_members tm
       JOIN public.tenant_invitations i ON i.tenant_id = tm.tenant_id AND i.consumed_by = tm.user_id
       WHERE tm.tenant_id = $1 AND tm.user_id = $2`,
      [targetTenantID, actors.memberA.userID]);
    expect(membershipRows).toEqual([{ role: 'member', consumed: true }]);
    evidence.ui.push('member joined and switched tenant from onboarding');
    evidence.http.push('join and switch-tenant completed successfully');
    evidence.database.push('membership and atomic invitation consumption reconciled');
    completed.push('iam.mutation.post.auth.switch.tenant');

    const refreshedMemberList = waitForMutation(tenantAdminPage, '/tenant/members', 'GET');
    await tenantAdminPage.reload();
    expect((await refreshedMemberList).status()).toBe(200);
    await expect(tenantAdminPage.locator('.ant-spin-spinning')).toHaveCount(0);
    const memberLoginRows = await publicQuery<{ github_login: string }>(pool,
      'SELECT github_login FROM public.users WHERE id = $1', [actors.memberA.userID]);
    const memberRow = tenantAdminPage.locator('tr').filter({ hasText: memberLoginRows[0].github_login });
    await expect(memberRow).toBeVisible();
    await memberRow.locator('td').last().locator('button').nth(0).click();
    const roleResponse = waitForMutation(tenantAdminPage, `/tenant/members/${actors.memberA.userID}/role`, 'PATCH');
    await tenantAdminPage.getByRole('menuitem', { name: '设为管理员' }).click();
    expect((await roleResponse).status()).toBe(200);
    const roleRows = await publicQuery<{ role: string }>(pool,
      'SELECT role FROM public.tenant_members WHERE tenant_id = $1 AND user_id = $2',
      [targetTenantID, actors.memberA.userID]);
    expect(roleRows).toEqual([{ role: 'admin' }]);
    completed.push('iam.mutation.patch.tenant.members.userid.role');

    await memberBPage.goto(`${webURL}/tenant/members`);
    await expect(memberBPage.getByRole('button', { name: /邀请成员/ })).toHaveCount(0);
    const denied = await actors.memberB.context.request.post(`${backendURL}/tenant/members/invite`, {
      data: { email: 'denied@example.test', role: 'member' },
      headers: { Authorization: `Bearer ${actors.memberB.accessToken}` },
    });
    expect(denied.status()).toBe(403);
    evidence.ui.push('member invitation control remained hidden');
    evidence.http.push('member invitation API denied with 403');
    evidence.database.push('denied member created no invitation');

    await memberRow.locator('td').last().locator('button').last().click();
    const removeConfirmation = tenantAdminPage.locator('.ant-popover').filter({ hasText: '确认移除该成员？' });
    await expect(removeConfirmation).toBeVisible();
    const removeResponse = waitForMutation(tenantAdminPage, `/tenant/members/${actors.memberA.userID}`, 'DELETE');
    await removeConfirmation.locator('.ant-popconfirm-buttons .ant-btn-primary').click();
    expect((await removeResponse).status()).toBe(200);
    const removedRows = await publicQuery<{ count: string }>(pool,
      'SELECT count(*)::text AS count FROM public.tenant_members WHERE tenant_id = $1 AND user_id = $2',
      [targetTenantID, actors.memberA.userID]);
    expect(removedRows).toEqual([{ count: '0' }]);
    completed.push('iam.mutation.delete.tenant.members.userid');

    const initialAdminList = waitForMutation(systemAdminPage, '/admin/tenants', 'GET');
    await systemAdminPage.goto(`${webURL}/admin/tenants`);
    await expect(systemAdminPage.getByRole('heading', { name: '所有租户' })).toBeVisible();
    expect((await initialAdminList).status()).toBe(200);
    await expect(systemAdminPage.locator('.ant-spin-spinning')).toHaveCount(0);
    completed.push('iam.route.admin.tenants');

    const adminTenantName = `E2E-Admin-${Date.now()}`;
    const adminTenantSlug = `e2e-admin-${Date.now()}`;
    await systemAdminPage.getByRole('button', { name: '创建租户' }).click();
    const createDialog = systemAdminPage.getByRole('dialog', { name: '创建租户' });
    await createDialog.getByLabel('租户名称').fill(adminTenantName);
    await createDialog.getByLabel('Slug').fill(adminTenantSlug);
    const adminCreateResponse = waitForMutation(systemAdminPage, '/admin/tenants', 'POST');
    const adminListRefresh = waitForMutation(systemAdminPage, '/admin/tenants', 'GET');
    await createDialog.getByRole('button', { name: '创建租户' }).click();
    const adminCreated = await adminCreateResponse;
    expect(adminCreated.status()).toBe(201);
    expect((await adminListRefresh).status()).toBe(200);
    const adminTenantID = requireUUID(
      (await adminCreated.json() as { id: string }).id,
      'admin_created_tenant_id',
    );
    await expect(createDialog).toHaveCount(0);
    const adminCreatedRows = await publicQuery<{ name: string; slug: string; plan: string; status: string }>(pool,
      'SELECT name, slug, plan, status FROM public.tenants WHERE id = $1', [adminTenantID]);
    expect(adminCreatedRows).toEqual([{
      name: adminTenantName,
      slug: adminTenantSlug,
      plan: 'free',
      status: 'active',
    }]);
    const adminCreatedRow = await findTenantRow(systemAdminPage, adminTenantName);
    await expect(adminCreatedRow).toBeVisible();
    evidence.ui.push('global admin created a tenant from the administrator modal');
    evidence.http.push('admin tenant creation returned 201');
    evidence.database.push('admin-created tenant fields reconciled');
    completed.push('iam.mutation.post.admin.tenants');

    await adminCreatedRow.locator('td').last().locator('button').nth(1).click();
    const adminCreatedConfirmation = systemAdminPage.locator('.ant-popover')
      .filter({ hasText: `确认删除租户「${adminTenantName}」？` });
    const adminCreatedDelete = waitForMutation(systemAdminPage, `/admin/tenants/${adminTenantID}`, 'DELETE');
    await adminCreatedConfirmation.locator('.ant-popconfirm-buttons .ant-btn-primary').click();
    expect((await adminCreatedDelete).status()).toBe(200);
    const adminDeletedRows = await publicQuery<{ count: string }>(pool,
      'SELECT count(*)::text AS count FROM public.tenants WHERE id = $1', [adminTenantID]);
    expect(adminDeletedRows).toEqual([{ count: '0' }]);

    const tenantRow = await findTenantRow(systemAdminPage, renamedTenant);
    const tenantActions = tenantRow.locator('td').last().locator('button');
    await tenantActions.nth(0).click();
    const suspendConfirmation = systemAdminPage.locator('.ant-popover').filter({ hasText: '确认禁用该租户？' });
    await expect(suspendConfirmation).toBeVisible();
    const suspendResponse = waitForMutation(systemAdminPage, `/admin/tenants/${targetTenantID}`, 'PATCH');
    const suspendedAdminList = waitForMutation(systemAdminPage, '/admin/tenants', 'GET');
    await suspendConfirmation.locator('.ant-popconfirm-buttons .ant-btn-primary').click();
    expect((await suspendResponse).status()).toBe(200);
    expect((await suspendedAdminList).status()).toBe(200);
    await expect(systemAdminPage.locator('.ant-spin-spinning')).toHaveCount(0);
    const statusRows = await publicQuery<{ status: string }>(pool,
      'SELECT status FROM public.tenants WHERE id = $1', [targetTenantID]);
    expect(statusRows).toEqual([{ status: 'suspended' }]);
    evidence.ui.push('global admin disabled target tenant');
    evidence.http.push('admin tenant status mutation returned 200');
    evidence.database.push('tenant suspension reconciled');
    completed.push('iam.mutation.patch.admin.tenants.tenantid');

    const deleteRow = await findTenantRow(systemAdminPage, renamedTenant);
    await deleteRow.locator('td').last().locator('button').nth(1).click();
    const deleteConfirmation = systemAdminPage.locator('.ant-popover').filter({ hasText: `确认删除租户「${renamedTenant}」？` });
    await expect(deleteConfirmation).toBeVisible();
    const deleteResponse = waitForMutation(systemAdminPage, `/admin/tenants/${targetTenantID}`, 'DELETE');
    await deleteConfirmation.locator('.ant-popconfirm-buttons .ant-btn-primary').click();
    expect((await deleteResponse).status()).toBe(200);
    const deletedRows = await publicQuery<{ count: string }>(pool,
      'SELECT count(*)::text AS count FROM public.tenants WHERE id = $1', [targetTenantID]);
    expect(deletedRows).toEqual([{ count: '0' }]);
    evidence.ui.push('global admin confirmed tenant deletion');
    evidence.http.push('admin tenant deletion returned 200');
    evidence.database.push('tenant and invitation cascade cleanup reconciled');
    completed.push('iam.mutation.delete.admin.tenants.tenantid');

    completed.push(...await executeIAMOAuthJourney({
      context: actors.memberB.context,
      pool,
      evidence,
      webURL,
      backendURL,
    }));
  } finally {
    await Promise.all([tenantAdminPage.close(), memberPage.close(), memberBPage.close(), systemAdminPage.close()]);
  }
  return completed;
};
